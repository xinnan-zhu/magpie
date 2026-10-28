package sshremote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/daemon"
)

const Protocol = 1
const MaxMessage = 4 << 20

// Context is reported by the process that actually serves the remote setup.
type Context struct {
	Version  string            `json:"version"`
	OS       string            `json:"os"`
	Arch     string            `json:"arch"`
	User     string            `json:"user"`
	UID      string            `json:"uid"`
	Hostname string            `json:"hostname"`
	Home     string            `json:"home"`
	Config   string            `json:"config"`
	Cache    string            `json:"cache"`
	Binary   string            `json:"binary"`
	Gateway  string            `json:"gateway,omitempty"`
	Roots    map[string]string `json:"roots"`
	Target   string            `json:"target"`
}

// ReadContext performs no initialization, account discovery or agent probes.
func ReadContext(version string) (Context, error) {
	home, err := appdir.Home()
	if err != nil {
		return Context{}, err
	}
	u, err := user.Current()
	if err != nil {
		return Context{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Context{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return Context{}, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return Context{}, err
	}
	c := Context{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, User: u.Username, UID: u.Uid, Hostname: hostname, Home: home, Config: appdir.Config(), Cache: appdir.Cache(), Binary: exe, Roots: map[string]string{}}
	for _, k := range agentenv.Vars {
		if !agentenv.NotPaths[k] {
			if v := appdir.Getenv(k); v != "" {
				c.Roots[k] = v
			}
		}
	}
	b, _ := json.Marshal([]any{c.Hostname, c.UID, c.Home, c.Config, c.Roots})
	sum := sha256.Sum256(b)
	c.Target = hex.EncodeToString(sum[:])
	return c, nil
}

type Inspection struct {
	Protocol   int           `json:"protocol"`
	Installed  bool          `json:"installed"`
	Compatible bool          `json:"compatible"`
	Context    Context       `json:"context"`
	Shell      *Context      `json:"shell,omitempty"`
	Service    daemon.Status `json:"service"`
	WebPort    int           `json:"webPort,omitempty"`
	Ready      bool          `json:"ready"`
	Problem    string        `json:"problem,omitempty"`
	// Proxy is what a service installed now would use; looked for only
	// while there is no service to install it into.
	Proxy *Proxy `json:"proxy,omitempty"`
}

// ServiceInstall is service-install's Body. Proxy false installs the service
// with no proxy; left out, it is given the one found (DetectProxy).
type ServiceInstall struct {
	Proxy *bool `json:"proxy,omitempty"`
}

type Request struct {
	Method string          `json:"method"`
	Target string          `json:"target,omitempty"`
	Path   string          `json:"path,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ServeControl is an early CLI entry point. Reads never run ordinary CLI startup.
// Business mutations are sent to the existing, authenticated web service, so the
// service's actual environment and original adapters perform the operation.
func ServeControl(ctx context.Context, in io.Reader, out io.Writer, version string) error {
	b, err := io.ReadAll(io.LimitReader(in, MaxMessage+1))
	if err != nil {
		return err
	}
	if len(b) > MaxMessage {
		return errors.New("SSH request is too large")
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil {
		return err
	}
	var result any
	switch req.Method {
	case "inspect":
		result, err = Inspect(ctx, version)
	case "service-install":
		c, e := ReadContext(version)
		if e != nil {
			err = e
			break
		}
		if req.Target == "" || req.Target != c.Target {
			err = errors.New("remote environment changed; reconnect")
			break
		}
		st, e := daemon.Get()
		if e != nil {
			err = e
			break
		}
		if st.Installed {
			err = errors.New("an existing service is installed; manage it from its remote Web page or CLI")
			break
		}
		var o ServiceInstall
		if req.Body != nil {
			if e := json.Unmarshal(req.Body, &o); e != nil {
				err = e
				break
			}
		}
		err = daemon.Install(daemon.Options{Web: "127.0.0.1:3430", Key: newID(), Proxy: DetectProxy(ctx).Use(o.Proxy == nil || *o.Proxy)})
		if err == nil {
			result, err = Inspect(ctx, version)
		}
	case "service-start":
		c, e := ReadContext(version)
		if e != nil {
			err = e
			break
		}
		if req.Target == "" || req.Target != c.Target {
			err = errors.New("remote environment changed; reconnect")
			break
		}
		err = daemon.Start()
		if err == nil {
			result, err = Inspect(ctx, version)
		}
	case "web-login":
		var inspection Inspection
		inspection, err = Inspect(ctx, version)
		if err == nil {
			if !inspection.Ready || req.Target != inspection.Context.Target {
				err = errors.New("the remote service context changed; reconnect")
			} else {
				_, key := daemon.Web()
				result = map[string]any{"key": key, "port": inspection.WebPort}
			}
		}
	case "api":
		if !allowedAPI(req.Path, req.Body != nil) {
			err = errors.New("unsupported SSH operation")
			break
		}
		var inspection Inspection
		inspection, err = Inspect(ctx, version)
		if err != nil {
			break
		}
		if !inspection.Ready || req.Target == "" || req.Target != inspection.Context.Target {
			err = errors.New("the remote service context changed or is unavailable; reconnect")
			break
		}
		result, err = webCall(ctx, req.Path, req.Body, req.Target)
	default:
		err = errors.New("unsupported SSH control method")
	}
	response := Response{}
	if err != nil {
		response.Error = err.Error()
	} else {
		response.Result, err = json.Marshal(result)
		if err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(response)
}

func Inspect(ctx context.Context, version string) (Inspection, error) {
	c, err := ReadContext(version)
	if err != nil {
		return Inspection{}, err
	}
	s := Inspection{Protocol: Protocol, Installed: true, Compatible: true, Context: c}
	s.Service, err = daemon.Get()
	if err != nil {
		s.Problem = err.Error()
		return s, nil
	}
	if !s.Service.Installed {
		s.Proxy = DetectProxy(ctx)
	}
	addr, key := daemon.Web()
	if addr == "" || key == "" {
		s.Problem = "the service has no authenticated Web page; use magpie daemon web on on the remote host"
		return s, nil
	}
	_, port, err := loopbackWeb(addr)
	if err != nil {
		s.Problem = err.Error()
		return s, nil
	}
	s.WebPort, _ = strconv.Atoi(port)
	raw, err := webCall(ctx, "/api/ssh/context", nil)
	if err != nil {
		s.Problem = "the remote Web service is stopped, incompatible or cannot be authenticated"
		return s, nil
	}
	var served Context
	if err = json.Unmarshal(raw, &served); err != nil || served.Target == "" {
		s.Problem = "update the remote Magpie for SSH control"
		return s, nil
	}
	s.Shell = &c
	s.Context = served
	s.Ready = true
	return s, nil
}

func allowedAPI(path string, write bool) bool {
	if !write {
		return path == "/api/ssh/agents" || path == "/api/ssh/transfer" || path == "/api/ssh/preview/claude" || path == "/api/ssh/preview/codex"
	}
	return path == "/api/ssh/pick" || path == "/api/ssh/agent" || path == "/api/ssh/transfer/plan" || path == "/api/ssh/transfer/apply" || path == "/api/ssh/upstream"
}

func loopbackWeb(addr string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return "", "", errors.New("SSH control requires a loopback Web service")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return "", "", errors.New("invalid Web port")
	}
	return host, port, nil
}

var webClient = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func webCall(ctx context.Context, path string, body json.RawMessage, targets ...string) (json.RawMessage, error) {
	addr, key := daemon.Web()
	_, port, err := loopbackWeb(addr)
	if err != nil {
		return nil, err
	}
	method := "GET"
	if body != nil {
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: "magpie_web_" + port, Value: key})
	req.Header.Set("Content-Type", "application/json")
	if len(targets) > 0 {
		req.Header.Set("X-Magpie-SSH-Target", targets[0])
	}
	res, err := webClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxMessage+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxMessage {
		return nil, errors.New("SSH reply is too large")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("remote operation refused (%d): %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	if !json.Valid(b) {
		return nil, errors.New("invalid remote response")
	}
	return b, nil
}
