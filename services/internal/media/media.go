// Package media stores uploaded files on local disk.
package media

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/store"
)

type Service struct {
	DB      *sql.DB
	Dir     string
	MaxSize int64 // bytes
}

type Media struct {
	ID       string `json:"id"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256,omitempty"`
}

func (s *Service) Routes(mux *http.ServeMux, authed func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v1/media", authed(httpx.Handler(s.upload)))
	mux.HandleFunc("GET /v1/media/{id}", authed(httpx.Handler(s.download)))
}

var blockedTypes = map[string]bool{"text/html": true, "image/svg+xml": true, "application/javascript": true, "text/javascript": true}

func (s *Service) path(id string) string {
	return filepath.Join(s.Dir, id[len("med_"):len("med_")+2], id)
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxSize+1<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return httpx.E(http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("files must be at most %d MB", s.MaxSize>>20))
		}
		return httpx.BadRequest("send the file as multipart field 'file'")
	}
	defer file.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	mt := http.DetectContentType(head)
	if claimed := hdr.Header.Get("Content-Type"); claimed != "" && mt == "application/octet-stream" {
		if parsed, _, err := mime.ParseMediaType(claimed); err == nil {
			mt = parsed // trust the client only when sniffing is inconclusive (e.g. m4a, opus)
		}
	}
	mt = strings.SplitN(mt, ";", 2)[0]
	if blockedTypes[mt] || strings.HasPrefix(mt, "text/html") {
		mt = "application/octet-stream" // never serve active content
	}

	mid := store.NewID("med")
	p := s.path(mid)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, h), io.MultiReader(bytes.NewReader(head), io.LimitReader(file, s.MaxSize+1-int64(len(head)))))
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if size > s.MaxSize {
		os.Remove(tmp)
		return httpx.E(http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("files must be at most %d MB", s.MaxSize>>20))
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	name := filepath.Base(hdr.Filename)
	if len(name) > 200 {
		name = name[:200]
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO media(id, owner_id, mime, size, filename, created_at) VALUES (?,?,?,?,?,?)`,
		mid, id.UserID, mt, size, name, store.Now()); err != nil {
		os.Remove(p)
		return err
	}
	httpx.JSON(w, http.StatusCreated, Media{ID: mid, Mime: mt, Size: size, Filename: name, SHA256: hex.EncodeToString(h.Sum(nil))})
	return nil
}

// CanRead: the owner, anyone for avatars, or a member of a conversation where
// the file was shared.
func (s *Service) CanRead(r *http.Request, uid, mid string) (Media, bool, error) {
	var m Media
	var owner string
	err := s.DB.QueryRowContext(r.Context(), `SELECT id, mime, size, filename, owner_id FROM media WHERE id = ?`, mid).Scan(&m.ID, &m.Mime, &m.Size, &m.Filename, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	if owner == uid {
		return m, true, nil
	}
	var n int
	err = s.DB.QueryRowContext(r.Context(), `SELECT
		(SELECT COUNT(*) FROM users WHERE avatar_media_id = ?1) +
		(SELECT COUNT(*) FROM conversations WHERE avatar_media_id = ?1) +
		(SELECT COUNT(*) FROM messages m JOIN members mb ON mb.conversation_id = m.conversation_id
		  WHERE m.media_id = ?1 AND m.deleted = 0 AND mb.user_id = ?2)`, mid, uid).Scan(&n)
	return m, n > 0, err
}

func (s *Service) download(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	mid := r.PathValue("id")
	if !strings.HasPrefix(mid, "med_") || len(mid) < 8 {
		return httpx.ErrNotFound
	}
	m, ok, err := s.CanRead(r, id.UserID, mid)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.ErrNotFound
	}
	f, err := os.Open(s.path(mid))
	if err != nil {
		return httpx.ErrNotFound
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", m.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if m.Filename != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": m.Filename}))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
	return nil
}
