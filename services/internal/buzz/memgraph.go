package buzz

import (
	"context"
	"sync"
)

// MemoryGraph is an in-memory Graph for tests and local development.
type MemoryGraph struct {
	mu      sync.RWMutex
	friends map[[2]string]bool
	blocks  map[[2]string]bool
	passes  map[[2]string]*Pass
}

func NewMemoryGraph() *MemoryGraph {
	return &MemoryGraph{
		friends: map[[2]string]bool{},
		blocks:  map[[2]string]bool{},
		passes:  map[[2]string]*Pass{},
	}
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (g *MemoryGraph) AddFriends(a, b string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.friends[pair(a, b)] = true
}

func (g *MemoryGraph) Block(blocker, blocked string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocks[[2]string{blocker, blocked}] = true
}

func (g *MemoryGraph) Grant(p Pass) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.passes[[2]string{p.GrantorID, p.GranteeID}] = &p
}

func (g *MemoryGraph) Revoke(grantor, grantee string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.passes, [2]string{grantor, grantee})
}

func (g *MemoryGraph) AreFriends(_ context.Context, a, b string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.friends[pair(a, b)], nil
}

func (g *MemoryGraph) IsBlocked(_ context.Context, blocker, blocked string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.blocks[[2]string{blocker, blocked}], nil
}

func (g *MemoryGraph) Pass(_ context.Context, grantor, grantee string) (*Pass, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p := g.passes[[2]string{grantor, grantee}]
	if p == nil {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}
