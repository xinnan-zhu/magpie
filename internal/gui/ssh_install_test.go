package gui

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/sshremote"
	"github.com/yetone/magpie/internal/update"
)

func TestSSHOneClickInstallFromVerifiedFeed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SSH process fixture")
	}
	for _, badDigest := range []bool{false, true} {
		t.Run(fmt.Sprint("badDigest=", badDigest), func(t *testing.T) {
			home := sandboxHome(t)
			bin := filepath.Join(home, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// A fake SSH server accepts bytes or one JSON control request. Actual
			// remote upload-script execution is covered in sshremote's tests.
			fixture := `#!/bin/sh
set -eu
body="$HOME/request"
cat > "$body"
if [ "$(head -c 4 "$body")" = "$(printf '\177ELF')" ]; then
  cp "$body" "$HOME/uploaded"
  exit 0
fi
if grep -q '"method":"service-install"' "$body"; then
  grep -q '"target":"fixture-context"' "$body"
  printf 'install\n' >> "$HOME/service-installed"
fi
if [ -f "$HOME/uploaded" ]; then
  if [ -f "$HOME/service-installed" ]; then ready=true; else ready=false; fi
  printf '{"result":{"protocol":1,"installed":true,"compatible":true,"ready":%s,"context":{"os":"linux","arch":"amd64","target":"fixture-context"},"service":{"Installed":%s}}}\n' "$ready" "$ready"
else
  printf '{"result":{"protocol":1,"installed":false,"compatible":false,"context":{"os":"linux","arch":"amd64"}}}\n'
fi
`
			if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(fixture), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
			artifact := make([]byte, 64)
			copy(artifact, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
			binary.LittleEndian.PutUint16(artifact[16:], 2)
			binary.LittleEndian.PutUint16(artifact[18:], 62)
			binary.LittleEndian.PutUint32(artifact[20:], 1)
			binary.LittleEndian.PutUint16(artifact[52:], 64)
			sum := sha256.Sum256(artifact)
			digest := hex.EncodeToString(sum[:])
			if badDigest {
				digest = strings.Repeat("0", 64)
			}
			var feedURL string
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					json.NewEncoder(w).Encode(update.Release{Version: "0.0.0", Assets: map[string]update.Asset{"magpie-cli-linux-amd64": {URL: feedURL + "/binary", SHA256: digest}}})
				case "/binary":
					w.Write(artifact)
				default:
					http.NotFound(w, r)
				}
			}))
			defer feed.Close()
			feedURL = feed.URL
			t.Setenv("MAGPIE_UPDATE_FEED", feedURL+"/latest")
			sshremote.Desktop.Enable()
			defer sshremote.Desktop.Close()
			c, err := sshremote.Desktop.Connect(context.Background(), sshremote.Host{Name: "fixture", Target: "fixture"})
			if err != nil || c.Inspection.Installed {
				t.Fatal("missing installation", c, err)
			}
			mux := http.NewServeMux()
			sshRoutes(mux, &pickerWin{})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ssh/connections/"+c.ID+"/deploy", strings.NewReader(`{"start":true}`)))
			installed, readErr := os.ReadFile(filepath.Join(home, "service-installed"))
			if badDigest {
				if rec.Code == http.StatusOK || !os.IsNotExist(readErr) {
					t.Fatal("unverified release installed", rec.Code, string(installed), readErr)
				}
				if _, err := os.Stat(filepath.Join(home, "uploaded")); !os.IsNotExist(err) {
					t.Fatal("unverified bytes uploaded", err)
				}
				return
			}
			if rec.Code != http.StatusOK || readErr != nil || string(installed) != "install\n" {
				t.Fatal("one-click setup", rec.Code, rec.Body.String(), string(installed), readErr)
			}
			var status sshremote.Inspection
			if err := sshremote.Call(context.Background(), c.Host, sshremote.Request{Method: "inspect"}, &status); err != nil || !status.Ready {
				t.Fatal("service was not ready", status, err)
			}
		})
	}
}
