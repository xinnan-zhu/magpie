package sshremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) {
	if os.Getenv("MAGPIE_SSH_FIXTURE") == "1" {
		os.Exit(fakeSSH())
	}
	testenv.Main(m)
}
func sshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}
func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestHostsFailClosedAndValidate(t *testing.T) {
	sshHome(t)
	for _, target := range []string{"-oProxyCommand=evil", "a b", "a;touch /tmp/pwn", "a\ncommand", "a@b@c"} {
		if err := (Host{Name: "fixture", Target: target}).Validate(); err == nil {
			t.Fatal("accepted", target)
		}
	}
	h, err := SaveHost(Host{Name: "fixture", Target: "user@fixture"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(HostsPath())
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, err)
	}
	h.Port = 2222
	if _, err = SaveHost(h); err != nil {
		t.Fatal(err)
	}
	hosts, err := Hosts()
	if err != nil || len(hosts) != 1 || hosts[0].Port != 2222 {
		t.Fatal(hosts, err)
	}
	if err = os.WriteFile(HostsPath(), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = SaveHost(Host{Name: "second", Target: "second"}); err == nil {
		t.Fatal("wrote over broken host file")
	}
	b, _ := os.ReadFile(HostsPath())
	if string(b) != "broken" {
		t.Fatal(string(b))
	}
	if err = os.Remove(HostsPath()); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(HostsPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Hosts(); err == nil {
		t.Fatal("unreadable file treated as empty")
	}
}
func TestBootstrapPrefersManagedBinaryWithoutStartingOldCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX remote shell fixture")
	}
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	marker := filepath.Join(home, "old-started")
	put(t, filepath.Join(bin, "magpie"), "#!/bin/sh\nif [ \"$1\" = --help ]; then printf 'old help'; exit 0; fi\nprintf started > '"+marker+"'\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	run := func() Response {
		cmd := proc.Command("/bin/sh", "-c", bootstrap(Host{}))
		b, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var r Response
		if e = json.Unmarshal(b, &r); e != nil {
			t.Fatal(string(b), e)
		}
		return r
	}
	r := run()
	var s Inspection
	if err := json.Unmarshal(r.Result, &s); err != nil {
		t.Fatal(err)
	}
	if !s.Installed || s.Compatible || s.Context.OS == "" {
		t.Fatal(s)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("old binary started", err)
	}
	put(t, filepath.Join(home, ".local/lib/magpie/ssh/magpie"), "#!/bin/sh\nif [ \"$1\" = --help ]; then printf 'magpie ssh-control'; else printf '{\"result\":{\"protocol\":1,\"installed\":true,\"compatible\":true}}'; fi\n")
	r = run()
	if err := json.Unmarshal(r.Result, &s); err != nil || !s.Compatible {
		t.Fatal(s, err)
	}
}
func TestSystemSSHUsesJSONStdinAndKeepsTrust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX OpenSSH fixture")
	}
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	record := filepath.Join(home, "args")
	body := filepath.Join(home, "body")
	put(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+record+"'\ncat > '"+body+"'\nprintf '{\"result\":true}'\n")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	var out bool
	err := Call(context.Background(), Host{Name: "fixture", Target: "user@fixture", Port: 2222}, Request{Method: "api", Target: "context", Path: "/api/ssh/upstream", Body: json.RawMessage(`{"key":"stdin-only-secret"}`)}, &out)
	if err != nil || !out {
		t.Fatal(out, err)
	}
	args, _ := os.ReadFile(record)
	stdin, _ := os.ReadFile(body)
	if strings.Contains(string(args), "stdin-only-secret") || !strings.Contains(string(stdin), "stdin-only-secret") {
		t.Fatal("credential transport", string(args), string(stdin))
	}
	for _, s := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "ForwardAgent=no", "ControlPath=none", "user@fixture"} {
		if !strings.Contains(string(args), s) {
			t.Fatal("missing SSH option", s)
		}
	}
}
func TestControlContextIsReadOnlyAndBounded(t *testing.T) {
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	for _, name := range []string{"launchctl", "systemctl"} {
		put(t, filepath.Join(bin, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", bin)
	var out bytes.Buffer
	if err := ServeControl(context.Background(), strings.NewReader(`{"method":"inspect"}`), &out, "fixture"); err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Error != "" {
		t.Fatal(out.String(), err)
	}
	for _, folder := range []string{".config/magpie", ".claude", ".codex"} {
		if _, err := os.Stat(filepath.Join(home, folder)); !os.IsNotExist(err) {
			t.Fatal("probe initialized configuration", folder, err)
		}
	}
	if err := ServeControl(context.Background(), strings.NewReader(strings.Repeat(" ", MaxMessage+1)), &out, "fixture"); err == nil {
		t.Fatal("oversize accepted")
	}
	if allowedAPI("/api/settings", true) || allowedAPI("/api/state", false) || allowedAPI("/api/ssh/../settings", true) {
		t.Fatal("control allow list widened")
	}
}
func TestSSHErrorAndListenerScope(t *testing.T) {
	for _, tt := range []struct{ stderr, code string }{{"Permission denied", "needs_auth"}, {"Host key verification failed", "needs_trust"}, {"REMOTE HOST IDENTIFICATION HAS CHANGED", "host_key_changed"}, {"remote port forwarding failed", "forward_failed"}} {
		e := sshError(tt.stderr, errors.New("exit")).(*SSHError)
		if e.Code != tt.code {
			t.Fatal(e)
		}
	}
	for _, host := range []string{"00000000", "00000000000000000000000000000000", "0101A8C0"} {
		if _, err := checkListeners(" 0: "+host+":0D67 00000000:0000 0A", 3431); err == nil {
			t.Fatal("accepted public listener", host)
		}
	}
	if found, err := checkListeners(" 0: 0100007F:0D67 00000000:0000 0A", 3431); err != nil || !found {
		t.Fatal(found, err)
	}
}
func TestManagerClosesPendingConnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	marker := filepath.Join(home, "pending")
	put(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nprintf pending > '"+marker+"'\nexec /bin/sleep 30\n")
	t.Setenv("PATH", bin)
	var manager Manager
	manager.Enable()
	done := make(chan error, 1)
	go func() {
		_, e := manager.Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
		done <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			manager.Close()
			t.Fatal("fixture never started")
		case <-time.After(10 * time.Millisecond):
		}
	}
	manager.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed plugin accepted a connection")
		}
	case <-ctx.Done():
		t.Fatal("pending SSH outlived plugin")
	}
	manager.Enable()
	defer manager.Close()
	if _, e := manager.Get("old"); e == nil {
		t.Fatal("old connection survived lifecycle")
	}
}
