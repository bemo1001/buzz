package platform

import (
	"strings"
	"testing"
)

func TestWebhookSSRFBlocked(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:1/", "http://10.0.0.1:1/", "http://169.254.169.254/latest/meta-data"} {
		_, err := safeClient(false).Get(u)
		if err == nil || !strings.Contains(err.Error(), "private address") {
			t.Fatalf("%s: expected private address refusal, got %v", u, err)
		}
	}
}
