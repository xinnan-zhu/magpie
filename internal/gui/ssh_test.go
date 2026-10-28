package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/sshremote"
)

func TestSSHConfigHostsDesktopOnly(t *testing.T) {
	home := sandboxHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host fixture-alias\n  IdentityFile never-open-this-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := pluginrt.Set("ssh", true); err != nil {
		t.Fatal(err)
	}
	defer sshremote.Desktop.Close()
	for _, w := range []Windows{nil, webHost{}, &pickerWin{}} {
		mux := http.NewServeMux()
		sshRoutes(mux, w)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ssh/config-hosts", nil))
		if w == nil || isWeb(w) {
			if rec.Code != http.StatusForbidden {
				t.Fatal("non-desktop could read SSH configuration", rec.Code)
			}
			continue
		}
		var aliases []string
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &aliases) != nil || len(aliases) != 1 || aliases[0] != "fixture-alias" {
			t.Fatal("desktop aliases", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("SSH aliases may be cached")
		}
		if err := pluginrt.Set("ssh", false); err != nil {
			t.Fatal(err)
		}
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ssh/config-hosts", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatal("disabled plugin could read SSH configuration", rec.Code)
		}
	}
}

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
	for _, path := range []string{"/api/ssh/hosts", "/api/ssh/config-hosts"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal("Web page got desktop SSH control", path, w.Code)
		}
	}
}
