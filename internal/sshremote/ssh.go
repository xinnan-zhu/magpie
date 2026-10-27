package sshremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// SSHError's code tells the UI which failures need attention in a terminal.
// OpenSSH's stderr is deliberately not returned: it can contain configuration
// commands and identity paths. No request body or credential is logged.
type SSHError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *SSHError) Error() string { return e.Message }
func sshError(stderr string, err error) error {
	code, msg := "ssh_failed", "SSH failed; check this host with system OpenSSH in a terminal"
	switch {
	case errors.Is(err, context.Canceled):
		code, msg = "canceled", "SSH connection canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code, msg = "timeout", "SSH connection timed out"
	case strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		code, msg = "host_key_changed", "SSH host key changed; verify it in a terminal before reconnecting"
	case strings.Contains(stderr, "Host key verification failed"):
		code, msg = "needs_trust", "Verify this host's key with system SSH in a terminal"
	case strings.Contains(stderr, "Permission denied"), strings.Contains(stderr, "passphrase"):
		code, msg = "needs_auth", "Authenticate with system SSH or unlock your key in ssh-agent, then reconnect"
	case strings.Contains(stderr, "forwarding failed"), strings.Contains(stderr, "cannot listen"):
		code, msg = "forward_failed", "SSH forwarding was refused or the port is occupied"
	}
	return &SSHError{code, msg}
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// BaseArgs never weakens host trust, forwards an agent, or reuses an unrelated
// master connection. The final configuration (including ProxyJump) is OpenSSH's.
func BaseArgs(h Host, forward bool) []string {
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "PermitLocalCommand=no", "-o", "ExitOnForwardFailure=yes"}
	if !forward {
		args = append(args, "-o", "ClearAllForwardings=yes")
	} else {
		args = append(args, "-o", "ClearAllForwardings=no")
	}
	if h.Port != 0 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	return args
}

// A missing/older binary is probed with --help, which is side-effect free in
// supported Magpie versions. Never run an unknown subcommand on an old binary:
// ordinary CLI startup migrates configuration before dispatch.
func bootstrap(h Host) string {
	locate := `b="$HOME/.local/lib/magpie/ssh/magpie"; if [ ! -x "$b" ]; then b=$(command -v magpie 2>/dev/null || true); fi`
	if h.Binary != "" {
		locate = "b=" + quote(h.Binary)
	}
	return locate + `; if [ ! -x "$b" ]; then printf '{"result":{"protocol":1,"installed":false,"compatible":false,"context":{"os":"%s","arch":"%s"}}}\n' "$(uname -s)" "$(uname -m)"; elif "$b" --help 2>/dev/null | grep -q 'magpie ssh-control'; then exec "$b" ssh-control; else printf '{"result":{"protocol":1,"installed":true,"compatible":false,"context":{"os":"%s","arch":"%s"},"problem":"Update remote Magpie for SSH control"}}\n' "$(uname -s)" "$(uname -m)"; fi`
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.limit {
		b.Buffer.Write(p[:min(len(p), b.limit-b.Len())])
	}
	return n, nil
}

// Call runs one bounded request. It never retries a mutation after a lost reply.
func Call(ctx context.Context, h Host, req Request, out any) error {
	if err := h.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(b) > MaxMessage {
		return errors.New("SSH request is too large")
	}
	args := append(BaseArgs(h, false), "--", h.Target, bootstrap(h))
	cmd := proc.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = bytes.NewReader(b)
	stdout := &boundedBuffer{limit: MaxMessage + 1}
	stderr := &boundedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return sshError(stderr.String(), err)
	}
	if stdout.Len() > MaxMessage {
		return errors.New("SSH reply is too large")
	}
	var response Response
	if err = json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return &SSHError{"invalid_reply", "The remote shell returned an invalid SSH control reply"}
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return json.Unmarshal(response.Result, out)
}

type forward struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	err    error
}

func startForward(ctx context.Context, h Host, spec string) (*forward, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	// OpenSSH cannot clear configured forwards without also clearing our -L/-R.
	// Refuse that ambiguous configuration instead of opening extra listeners.
	config := proc.CommandContext(ctx, "ssh", append(BaseArgs(h, true), "-G", "--", h.Target)...)
	resolved, err := config.Output()
	if err != nil {
		return nil, sshError("", err)
	}
	for _, line := range strings.Split(string(resolved), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && (fields[0] == "localforward" || fields[0] == "remoteforward" || fields[0] == "dynamicforward") {
			return nil, errors.New("use an SSH Host alias without configured LocalForward, RemoteForward or DynamicForward")
		}
	}
	life, cancel := context.WithCancel(ctx)
	args := append(BaseArgs(h, true), "-N")
	args = append(args, spec[:2], spec[2:], "--", h.Target)
	cmd := proc.CommandContext(life, "ssh", args...)
	stderr := &boundedBuffer{limit: 8192}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	f := &forward{cancel: cancel, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		f.mu.Lock()
		if err != nil {
			f.err = sshError(stderr.String(), err)
		}
		f.mu.Unlock()
		close(f.done)
	}()
	return f, nil
}
func (f *forward) Close() { f.cancel(); <-f.done }
func (f *forward) Error() error {
	select {
	case <-f.done:
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.err != nil {
			return f.err
		}
		return errors.New("SSH forwarding stopped")
	default:
		return nil
	}
}

// Connection is bound to a snapshot, never to a mutable "current host".
type Connection struct {
	ID         string     `json:"id"`
	Host       Host       `json:"host"`
	Inspection Inspection `json:"inspection"`
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	forwards   []*forward
	browser    *http.Server
	webForward *forward
	closers    []func()
	tunnel     *Tunnel
	plan       *copyPlan
}

func Connect(ctx context.Context, h Host) (*Connection, error) {
	var s Inspection
	if err := Call(ctx, h, Request{Method: "inspect"}, &s); err != nil {
		return nil, err
	}
	if s.Installed && s.Compatible && s.Protocol != Protocol {
		return nil, errors.New("incompatible SSH control protocol; update remote Magpie")
	}
	if s.Ready && (!s.Compatible || s.Context.Target == "") {
		return nil, errors.New("invalid remote context")
	}
	life, cancel := context.WithCancel(context.Background())
	return &Connection{ID: newID(), Host: h, Inspection: s, ctx: life, cancel: cancel}, nil
}
func (c *Connection) Close() {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.browser != nil {
		c.browser.Close()
	}
	if c.webForward != nil {
		c.webForward.Close()
		c.webForward = nil
	}
	for _, f := range c.forwards {
		f.Close()
	}
	c.forwards = nil
	for _, close := range c.closers {
		close()
	}
	c.closers = nil
	c.plan = nil
}

// OperationContext bounds a management operation by both its caller and this
// connection's lifetime. Closing the plugin cancels pending SSH children too.
func (c *Connection) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	bound, cancel := context.WithTimeout(ctx, 45*time.Second)
	stop := context.AfterFunc(c.ctx, cancel)
	return bound, func() { stop(); cancel() }
}

func (c *Connection) Call(ctx context.Context, req Request, out any) error {
	ctx, cancel := c.OperationContext(ctx)
	defer cancel()
	return Call(ctx, c.Host, req, out)
}
func (c *Connection) API(ctx context.Context, path string, body any, out any) error {
	select {
	case <-c.ctx.Done():
		return errors.New("SSH connection is closed")
	default:
	}
	req := Request{Method: "api", Path: path, Target: c.Inspection.Context.Target}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req.Body = b
	}
	err := c.Call(ctx, req, out)
	if err != nil && body != nil {
		return fmt.Errorf("remote result unconfirmed; refresh before another action: %w", err)
	}
	return err
}

// OpenWeb forwards only the service's loopback Web port. A one-use local link
// signs in to a local proxy; the remote Web key never reaches the browser.
func (c *Connection) OpenWeb(ctx context.Context) (string, error) {
	ctx, cancelOp := c.OperationContext(ctx)
	defer cancelOp()
	if !c.Inspection.Ready {
		return "", errors.New("the remote Web service is unavailable")
	}
	var credential struct {
		Key  string `json:"key"`
		Port int    `json:"port"`
	}
	if err := Call(ctx, c.Host, Request{Method: "web-login", Target: c.Inspection.Context.Target}, &credential); err != nil {
		return "", err
	}
	if credential.Port < 1 || credential.Port > 65535 || credential.Key == "" {
		return "", errors.New("invalid remote Web credential")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	ln.Close()
	f, err := startForward(c.ctx, c.Host, "-L"+addr+":127.0.0.1:"+strconv.Itoa(credential.Port))
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return "", errors.New("SSH connection is closed")
	}
	// SSH can exit after starting. Wait for this particular forward's HTTP
	// authentication guard, not merely its process being alive.
	readyCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for {
		if err = f.Error(); err != nil {
			return "", err
		}
		req, _ := http.NewRequestWithContext(readyCtx, "GET", "http://"+addr+"/api/ssh/context", nil)
		req.AddCookie(&http.Cookie{Name: "magpie_web_" + strconv.Itoa(credential.Port), Value: credential.Key})
		res, e := client.Do(req)
		if e == nil {
			var got Context
			e = json.NewDecoder(io.LimitReader(res.Body, MaxMessage)).Decode(&got)
			res.Body.Close()
			if e == nil && res.StatusCode == 200 && got.Target == c.Inspection.Context.Target {
				break
			}
		}
		select {
		case <-readyCtx.Done():
			return "", errors.New("the forwarded remote Web service did not authenticate")
		case <-time.After(50 * time.Millisecond):
		}
	}
	login, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	ticket := newID()
	cookieName, cookieValue := "magpie_ssh_"+c.ID, newID()
	upstream, _ := url.Parse("http://" + addr)
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.Transport = &http.Transport{Proxy: nil}
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Header.Del("Cookie")
		r.AddCookie(&http.Cookie{Name: "magpie_web_" + strconv.Itoa(credential.Port), Value: credential.Key})
	}
	proxy.ModifyResponse = func(r *http.Response) error { r.Header.Del("Set-Cookie"); return nil }
	var once sync.Once
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != login.Addr().String() || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/"+ticket || r.Method != "GET" {
			if ck, e := r.Cookie(cookieName); e != nil || ck.Value != cookieValue {
				http.Error(w, "open this remote Web page from Magpie again", http.StatusUnauthorized)
				return
			}
			proxy.ServeHTTP(w, r)
			return
		}
		used := true
		once.Do(func() { used = false })
		if used {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookieValue, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "http://"+login.Addr().String()+"/", http.StatusSeeOther)
	})}
	if c.browser != nil {
		c.browser.Close()
		c.webForward.Close()
	}
	c.browser, c.webForward = srv, f
	keep = true
	go srv.Serve(login)
	return "http://" + login.Addr().String() + "/" + ticket, nil
}
