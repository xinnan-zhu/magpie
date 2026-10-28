package sshremote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/daemon"
	"github.com/yetone/magpie/internal/proc"
)

func TestSSHUploadKeepsPreviousAndRejectsOlderRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX remote shell")
	}
	home := sshHome(t)
	if runtime.GOOS == "darwin" {
		bin := filepath.Join(home, "bin")
		put(t, filepath.Join(bin, "sha256sum"), "#!/bin/sh\nexec /usr/bin/shasum -a 256 \"$@\"\n")
		t.Setenv("PATH", bin+":/usr/bin:/bin")
	}
	// Exercise the actual target-side script with a native executable fixture.
	// Local ELF/architecture validation is checked separately by Deploy's test.
	candidate := "#!/bin/sh\n[ \"$1\" = --help ] || exit 91\nprintf 'magpie ssh-control\\n'\n"
	upload := func(body string) error {
		sum := sha256.Sum256([]byte(body))
		cmd := proc.Command("/bin/sh", "-c", uploadScript(hex.EncodeToString(sum[:]), true))
		cmd.Stdin = strings.NewReader(body)
		return cmd.Run()
	}
	if err := upload(candidate); err != nil {
		t.Fatal(err)
	}
	if err := upload(candidate + "# next version\n"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local/lib/magpie/ssh")
	old, err := os.ReadFile(filepath.Join(dir, "magpie.previous"))
	if err != nil || string(old) != candidate {
		t.Fatal("previous installation was lost", err)
	}
	if err := upload("#!/bin/sh\nprintf 'old help\\n'\n"); err == nil {
		t.Fatal("accepted a release without SSH control")
	}
	sum := sha256.Sum256([]byte(candidate))
	cmd := proc.Command("/bin/sh", "-c", uploadScript(hex.EncodeToString(sum[:]), false))
	cmd.Stdin = strings.NewReader(candidate)
	if err := cmd.Run(); err == nil {
		t.Fatal("replaced an installation created since the initial probe")
	}
	current, err := os.ReadFile(filepath.Join(dir, "magpie"))
	if err != nil || string(current) != candidate+"# next version\n" {
		t.Fatal("older release replaced the managed binary", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatal("temporary upload or deployment lock was left behind", files, err)
	}
	st, err := os.Stat(filepath.Join(dir, "magpie"))
	if err != nil || st.Mode().Perm() != 0700 {
		t.Fatal(st, err)
	}
}

// Each request runs in its own child, like system SSH. The file is the fake
// service manager's state; nothing reads or writes a real service definition.
func fakeInstallation(req Request, scenario string) int {
	home := os.Getenv("HOME")
	marker := filepath.Join(home, "service-installed")
	_, statErr := os.Stat(marker)
	s := Inspection{Protocol: Protocol, Installed: true, Compatible: true, Context: Context{Target: "fixture-context"}, Service: daemon.Status{Installed: statErr == nil}}
	if scenario == "existing" {
		s.Service.Installed = true
	}
	if scenario == "unsupported" {
		s.Protocol++
	}
	switch req.Method {
	case "inspect":
		s.Ready = statErr == nil && scenario != "waiting"
	case "service-install":
		if req.Target != s.Context.Target {
			return 7
		}
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return 8
		}
		f.WriteString("install\n")
		f.Close()
		if scenario == "lost-reply" {
			os.Stdout.WriteString("reply lost")
			return 0
		}
	default:
		return 9
	}
	b, _ := json.Marshal(s)
	json.NewEncoder(os.Stdout).Encode(Response{Result: b})
	return 0
}

func TestSSHInstallServiceChecksContextWaitsAndNeverRetries(t *testing.T) {
	for _, scenario := range []string{"ready", "existing", "unsupported", "lost-reply", "waiting"} {
		t.Run(scenario, func(t *testing.T) {
			fakeSystemSSH(t)
			t.Setenv("MAGPIE_SSH_INSTALL_FIXTURE", scenario)
			home := os.Getenv("HOME")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "waiting" {
				done := make(chan struct{})
				go func() {
					defer close(done)
					for {
						if _, err := os.Stat(filepath.Join(home, "service-installed")); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}()
				defer func() { cancel(); <-done }()
			}
			bounded, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			s, err := InstallService(bounded, Host{Name: "fixture", Target: "fixture"})
			if scenario == "ready" {
				if err != nil || !s.Ready {
					t.Fatal("did not wait for the authenticated Web service", s, err)
				}
			} else if err == nil {
				t.Fatal("unexpected installation success", scenario)
			}
			b, readErr := os.ReadFile(filepath.Join(home, "service-installed"))
			if scenario == "existing" || scenario == "unsupported" {
				if !os.IsNotExist(readErr) {
					t.Fatal("changed an existing or unsupported service", string(b), readErr)
				}
			} else if readErr != nil || string(b) != "install\n" {
				t.Fatal("installation was missing or retried", string(b), readErr)
			}
		})
	}
}
