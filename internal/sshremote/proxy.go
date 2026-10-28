package sshremote

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/daemon"
	"github.com/yetone/magpie/internal/netproxy"
	"github.com/yetone/magpie/internal/proc"
)

// Proxy is the proxy a remote service installed now would be given, and
// where it was found. A user service starts from the service manager's
// environment and what `daemon install` carries from the process installing
// it, which is this SSH command's: a non-interactive shell's, which reads
// none of the files a terminal sets a proxy in (~/.bashrc past its
// interactive guard, ~/.profile, ~/.zshrc). So the proxy is looked for where
// the service would find it, then in the user's own shell.
type Proxy struct {
	// Source is where it was found: "session" (this SSH command's
	// environment, which the service carries), "systemd" (the user manager's
	// environment, which the service inherits) or "shell" (a login,
	// interactive shell, which the service gets only when it is given it).
	Source string `json:"source"`
	// URL is the proxy as the page shows it, its user and password left out.
	URL string `json:"url"`
	// vars stay on the remote host: a proxy's address can hold a password.
	vars map[string]string
}

// shellWait bounds the login shell: its startup files are the user's own,
// and one that starts something which never ends (a tmux attach) is cut off.
var shellWait = 5 * time.Second

// proxyMark divides what a shell's startup files printed from its env.
const proxyMark = "--magpie-ssh-proxy--"

// DetectProxy finds the proxy in the order the service would meet it; nil
// when there is none.
func DetectProxy(ctx context.Context) *Proxy {
	if p := proxyFrom("session", sessionProxy()); p != nil {
		return p
	}
	if p := proxyFrom("systemd", systemdProxy(ctx)); p != nil {
		return p
	}
	return proxyFrom("shell", shellProxy(ctx))
}

// Use is what the service is installed with (daemon.Options.Proxy): nil
// keeps what it finds by itself, a session's or the user manager's; a shell's
// is given to it; refused, every proxy variable is set empty.
func (p *Proxy) Use(use bool) map[string]string {
	if !use {
		return map[string]string{}
	}
	if p == nil || p.Source != "shell" {
		return nil
	}
	return p.vars
}

func proxyFrom(source string, vars map[string]string) *Proxy {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := vars[k]; v != "" {
			return &Proxy{Source: source, URL: netproxy.Redact(v), vars: vars}
		}
	}
	return nil
}

func sessionProxy() map[string]string {
	vars := map[string]string{}
	for _, k := range daemon.ProxyVars {
		if v := os.Getenv(k); v != "" {
			vars[k] = v
		}
	}
	return vars
}

func systemdProxy(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := proc.ProbeContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil {
		return nil
	}
	return proxyLines(string(out))
}

func shellProxy(ctx context.Context) map[string]string {
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, shellWait)
	defer cancel()
	// stdin is /dev/null, so nothing in the startup files waits on input
	out, _ := proc.ProbeContext(ctx, shell, "-l", "-i", "-c", "echo "+proxyMark+"; env").Output()
	_, env, ok := strings.Cut(string(out), proxyMark+"\n")
	if !ok {
		return nil
	}
	return proxyLines(env)
}

// proxyLines are the proxy variables of env's KEY=value lines. systemd
// writes a value with characters a shell would read as $'…'.
func proxyLines(env string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(env, "\n") {
		k, v, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if !ok || !slices.Contains(daemon.ProxyVars, k) {
			continue
		}
		if strings.HasPrefix(v, "$'") && strings.HasSuffix(v, "'") {
			v = v[2 : len(v)-1]
		}
		if v != "" {
			vars[k] = v
		}
	}
	return vars
}
