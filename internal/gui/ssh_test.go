package gui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/sshremote"
)

func TestSSHRemoteRoutesCheckActualContextOnEveryRequest(t *testing.T) {
	sandboxHome(t)
	mux := http.NewServeMux()
	sshRoutes(mux, nil)
	c, e := sshremote.ReadContext(Version)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/ssh/agents", "/api/ssh/transfer"} {
		for _, target := range []string{"", "previous-context"} {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("X-Magpie-SSH-Target", target)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != 409 {
				t.Fatalf("%s target %q got %d", path, target, w.Code)
			}
		}
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Magpie-SSH-Target", c.Target)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("current context %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/ssh/hosts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("Web page got desktop SSH control", w.Code)
	}
}
