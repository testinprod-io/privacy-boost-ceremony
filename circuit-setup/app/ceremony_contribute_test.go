package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadInputArtifactSendsQueryAndHeaderAuth(t *testing.T) {
	const token = "session-token-123"
	const payload = "phase2-input"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sessionToken"); got != token {
			t.Fatalf("expected sessionToken query param %q, got %q", token, got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("expected Authorization header %q, got %q", "Bearer "+token, got)
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "input.ph2")
	err := downloadInputArtifact(
		srv.URL,
		"/v1/contribute/input/s1/s1-lease",
		token,
		dst,
		verbosity{quiet: true},
	)
	if err != nil {
		t.Fatalf("downloadInputArtifact returned error: %v", err)
	}

	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded artifact: %v", err)
	}
	if string(b) != payload {
		t.Fatalf("expected payload %q, got %q", payload, string(b))
	}
}
