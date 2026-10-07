package chatpage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The page is one self-contained document: served as HTML, and reading what
// it shows from the simulation API rather than from any outside address.
func TestServeWritesTheSelfContainedPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Serve(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body, _ := io.ReadAll(rec.Result().Body)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	page := string(body)
	if !strings.Contains(page, "<html") || !strings.Contains(page, "/sim/") {
		t.Fatalf("not the chat page: %.200s", page)
	}
	for _, outside := range []string{"<script src=\"http", "<link rel=\"stylesheet\" href=\"http"} {
		if strings.Contains(page, outside) {
			t.Fatalf("the page loads an outside asset (%s)", outside)
		}
	}
}
