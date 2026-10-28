//go:build !windows

package sshremote

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/daemon"
)

// proxyHome is a remote host with no proxy anywhere: none in this process's
// environment, a systemctl whose user environment is systemd, and a login
// shell that sets none. Each case puts one back.
func proxyHome(t *testing.T, systemd, shell string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX remote shell")
	}
	home := sshHome(t)
	for _, k := range daemon.ProxyVars {
		t.Setenv(k, "")
	}
	bin := filepath.Join(home, "bin")
	put(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\n[ \"$*\" = '--user show-environment' ] || exit 2\nprintf '%s' "+quote(systemd)+"\n")
	put(t, filepath.Join(home, "login-shell"), "#!/bin/sh\n"+shell)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", filepath.Join(home, "login-shell"))
}

// The shell is asked as a terminal's would be: a login, interactive one,
// whatever its startup files print first.
const loginShell = `[ "$1 $2 $3" = "-l -i -c" ] || exit 3
echo 'Welcome back'
export https_proxy=http://user:secret@127.0.0.1:7890 no_proxy=localhost
exec /bin/sh -c "$4"
`

func TestProxyFoundWhereTheServiceWouldMeetIt(t *testing.T) {
	ctx := context.Background()
	t.Run("none", func(t *testing.T) {
		proxyHome(t, "LANG=C\n", "exec /bin/sh -c \"$4\"\n")
		if p := DetectProxy(ctx); p != nil {
			t.Fatal("found a proxy where there is none", p)
		}
	})
	t.Run("session", func(t *testing.T) {
		proxyHome(t, "HTTPS_PROXY=http://systemd:1\n", loginShell)
		t.Setenv("ALL_PROXY", "socks5://user:pass@127.0.0.1:1080")
		p := DetectProxy(ctx)
		if p == nil || p.Source != "session" || p.URL != "socks5://127.0.0.1:1080" {
			t.Fatal("the SSH session's proxy, which the service carries, comes first", p)
		}
		if p.Use(true) != nil {
			t.Fatal("a proxy the service carries by itself is given again")
		}
	})
	t.Run("systemd", func(t *testing.T) {
		proxyHome(t, "LANG=C\nHTTPS_PROXY=$'http://127.0.0.1:3128'\nNO_PROXY=10.0.0.0/8\n", loginShell)
		p := DetectProxy(ctx)
		if p == nil || p.Source != "systemd" || p.URL != "http://127.0.0.1:3128" {
			t.Fatal("the user manager's proxy, which the service inherits, comes before the shell's", p)
		}
		if p.Use(true) != nil {
			t.Fatal("an inherited proxy is given again")
		}
	})
	t.Run("shell", func(t *testing.T) {
		proxyHome(t, "LANG=C\n", loginShell)
		p := DetectProxy(ctx)
		if p == nil || p.Source != "shell" || p.URL != "http://127.0.0.1:7890" {
			t.Fatal("the login shell's proxy, its password left out of what is shown", p)
		}
		if b, _ := json.Marshal(p); strings.Contains(string(b), "secret") {
			t.Fatal("the proxy's password leaves the remote host", string(b))
		}
		got := p.Use(true)
		if got["https_proxy"] != "http://user:secret@127.0.0.1:7890" || got["no_proxy"] != "localhost" {
			t.Fatal("the service is not given the shell's proxy", got)
		}
		if off := p.Use(false); off == nil || len(off) != 0 {
			t.Fatal("refused, the service must get no proxy at all", off)
		}
	})
	t.Run("shell that never ends", func(t *testing.T) {
		// its wait is the inspection's context ending, as shellWait's timeout
		// is; ended here once the shell is running, whatever the machine's load
		old := shellWait
		shellWait = time.Minute
		t.Cleanup(func() { shellWait = old })
		// what the startup files started (a tmux, an agent) ends with it too
		proxyHome(t, "", "sleep 600 &\necho $! > \"$HOME/shell.pid\"\nwait\n")
		inspect, end := context.WithCancel(ctx)
		defer end()
		found := make(chan *Proxy, 1)
		go func() { found <- DetectProxy(inspect) }()
		pidFile := filepath.Join(os.Getenv("HOME"), "shell.pid")
		var pid int
		for deadline := time.Now().Add(30 * time.Second); pid <= 0; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the login shell never started")
			}
			b, _ := os.ReadFile(pidFile)
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		end()
		select {
		case p := <-found:
			if p != nil {
				t.Fatal(p)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a shell that never ends held the inspection after it ended")
		}
		for deadline := time.Now().Add(10 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("what the shell started outlived the inspection", pid)
			}
		}
	})
}

// Refusing the proxy is said in the request; using it is the default, so a
// remote Magpie from before the choice installs as it did.
func TestServiceInstallRequestSaysOnlyARefusal(t *testing.T) {
	if r := ServiceInstallRequest("ctx", true); r.Body != nil || r.Method != "service-install" || r.Target != "ctx" {
		t.Fatal(r)
	}
	r := ServiceInstallRequest("ctx", false)
	var o ServiceInstall
	if err := json.Unmarshal(r.Body, &o); err != nil || o.Proxy == nil || *o.Proxy {
		t.Fatal(string(r.Body), err)
	}
}
