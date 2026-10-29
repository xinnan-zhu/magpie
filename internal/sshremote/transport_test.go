package sshremote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/provider"
)

// fakeSSH is an actual child process with OpenSSH's argument shape. It only
// forwards fixture loopback ports, and never opens an SSH connection.
func fakeSSH() int {
	args := os.Args
	for _, a := range args {
		if a == "-G" {
			os.Stdout.WriteString("hostname fixture\n")
			return 0
		}
	}
	if args[len(args)-1] == preparePrivateForward {
		// Match mktemp's exact ten-character suffix; MkdirTemp's decimal
		// suffix can have fewer characters and is not this shell's output.
		var suffix [5]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return 8
		}
		dir := "/tmp/magpie-ssh." + hex.EncodeToString(suffix[:])
		if err := os.Mkdir(dir, 0700); err != nil {
			return 8
		}
		if os.WriteFile(filepath.Join(os.Getenv("HOME"), "private-forward-dir"), []byte(dir), 0600) != nil {
			return 8
		}
		os.Stdout.WriteString(dir + "\n")
		return 0
	}
	if strings.HasPrefix(args[len(args)-1], "rm -rf -- '/tmp/magpie-ssh.") {
		if proc.Command("/bin/sh", "-c", args[len(args)-1]).Run() != nil {
			return 8
		}
		return 0
	}
	for n, a := range args {
		if a != "-L" && a != "-R" {
			continue
		}
		spec := strings.Split(args[n+1], ":")
		if len(spec) != 4 && len(spec) != 3 {
			return 2
		}
		address, target, network := spec[0], spec[1]+":"+spec[2], "unix"
		if len(spec) == 4 {
			address, target, network = spec[0]+":"+spec[1], spec[2]+":"+spec[3], "tcp"
		}
		ln, e := net.Listen(network, address)
		if e != nil {
			return 3
		}
		defer ln.Close()
		serve := func() {
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				go func() {
					defer c.Close()
					other, e := net.Dial("tcp", target)
					if e != nil {
						return
					}
					defer other.Close()
					done := make(chan struct{})
					go func() {
						io.Copy(c, other)
						c.(interface{ CloseWrite() error }).CloseWrite()
						close(done)
					}()
					io.Copy(other, c)
					other.(*net.TCPConn).CloseWrite()
					<-done
				}()
			}
		}
		if network == "unix" {
			go serve()
			cmd := proc.Command("/bin/sh", "-c", args[len(args)-1])
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if cmd.Run() != nil {
				return 9
			}
			return 0
		}
		serve()
		return 0
	}
	var req Request
	if e := json.NewDecoder(os.Stdin).Decode(&req); e != nil {
		return 4
	}
	if scenario := os.Getenv("MAGPIE_SSH_INSTALL_FIXTURE"); scenario != "" {
		return fakeInstallation(req, scenario)
	}
	var out any
	switch req.Method {
	case "inspect":
		out = Inspection{Protocol: Protocol, Installed: true, Compatible: true, Ready: true, Context: Context{Target: "fixture-context", OS: "linux"}}
	case "web-login":
		port, _ := strconv.Atoi(os.Getenv("MAGPIE_SSH_FIXTURE_PORT"))
		out = map[string]any{"key": "fixture-web-secret", "port": port}
	case "api":
		url := os.Getenv("MAGPIE_SSH_FIXTURE_CONTROL")
		if !strings.HasPrefix(url, "http://127.0.0.1:") {
			return 5
		}
		r, err := http.NewRequest("POST", url+req.Path, strings.NewReader(string(req.Body)))
		if err != nil {
			return 6
		}
		r.Header.Set("X-Magpie-SSH-Target", req.Target)
		client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
		res, err := client.Do(r)
		if err != nil {
			return 7
		}
		defer res.Body.Close()
		io.Copy(os.Stdout, res.Body)
		return 0
	default:
		return 5
	}
	b, _ := json.Marshal(out)
	json.NewEncoder(os.Stdout).Encode(Response{Result: b})
	return 0
}
func fakeSystemSSH(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child-process wrapper")
	}
	home := sshHome(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(home, "bin")
	put(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nexec "+quote(exe)+" -test.run=^$ -- \"$@\"\n")
	t.Setenv("MAGPIE_SSH_FIXTURE", "1")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
}

func TestSSHAPIRefusalIsConfirmedButLostReplyIsUnknown(t *testing.T) {
	fakeSystemSSH(t)
	for _, tc := range []struct {
		name, reply string
		uncertain   bool
	}{
		{"legacy no_models", `{"error":"remote operation refused (400): {\"agent\":\"Codex\",\"code\":\"no_models\",\"error\":\"Add a provider or subscription in magpie first, then connect Codex\"}"}`, false},
		{"structured no_models", `{"error":"Add a provider or subscription in magpie first, then connect Codex","failure":{"status":400,"code":"no_models","agent":"Codex","error":"Add a provider or subscription in magpie first, then connect Codex"}}`, false},
		{"server failure", `{"error":"remote operation refused (500): write failed"}`, true},
		{"partial write", `{"error":"remote operation refused (400): write failed"}`, true},
		{"truncated reply", `{"result":`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.reply))
			}))
			defer remote.Close()
			t.Setenv("MAGPIE_SSH_FIXTURE_CONTROL", remote.URL)
			c, err := Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var out json.RawMessage
			err = c.API(context.Background(), "/api/ssh/agent", map[string]string{"agent": "codex", "action": "connect"}, &out)
			if err == nil {
				t.Fatal("failed operation reported success")
			}
			if strings.Contains(err.Error(), "result unconfirmed") != tc.uncertain {
				t.Fatalf("wrong outcome certainty: %v", err)
			}
			if !tc.uncertain && err.Error() != "Add a provider or subscription in magpie first, then connect Codex" {
				t.Fatalf("refusal is not directly readable: %v", err)
			}
		})
	}
}
func TestForwardedWebAuthenticatesAndCloses(t *testing.T) {
	fakeSystemSSH(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, e := r.Cookie("magpie_web_" + os.Getenv("MAGPIE_SSH_FIXTURE_PORT"))
		if e != nil || cookie.Value != "fixture-web-secret" {
			http.Error(w, "no key", 401)
			return
		}
		json.NewEncoder(w).Encode(Context{Target: "fixture-context"})
	}))
	defer remote.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(remote.URL, "http://"))
	t.Setenv("MAGPIE_SSH_FIXTURE_PORT", port)
	c, e := Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	link, e := c.OpenWeb(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(link, "fixture-web-secret") {
		t.Fatal("web key in login URL")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Get(link)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatal(res.Status)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Value == "fixture-web-secret" || !cookies[0].HttpOnly {
		t.Fatal("cookie missing")
	}
	forwarded := res.Header.Get("Location")
	req, _ := http.NewRequest("GET", forwarded+"api/ssh/context", nil)
	req.AddCookie(cookies[0])
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
	res, e = client.Get(link)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("login ticket reused", res.Status)
	}
	c.Close()
	if res, e = client.Get(forwarded); e == nil {
		res.Body.Close()
		t.Fatal("forward survived connection close")
	}
}
func TestDeployChecksDigestAndArchitectureBeforeSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX remote upload")
	}
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	record := filepath.Join(home, "uploaded")
	put(t, filepath.Join(bin, "ssh"), "#!/bin/sh\ncat > "+quote(record)+"\n")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], 2)
	binary.LittleEndian.PutUint16(data[18:], 62)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint16(data[52:], 64)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	h := Host{Name: "fixture", Target: "fixture"}
	s := Inspection{Context: Context{OS: "linux", Arch: "amd64"}}
	if e := Deploy(context.Background(), h, s, data, strings.Repeat("0", 64)); e == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, e := os.Stat(record); !os.IsNotExist(e) {
		t.Fatal("upload ran before validation", e)
	}
	if e := Deploy(context.Background(), h, s, data, digest); e != nil {
		t.Fatal(e)
	}
	uploaded, e := os.ReadFile(record)
	if e != nil || string(uploaded) != string(data) {
		t.Fatal("wrong upload", e)
	}
	if e := Deploy(context.Background(), h, Inspection{Context: Context{OS: "linux", Arch: "arm64"}}, data, digest); e == nil {
		t.Fatal("wrong architecture accepted")
	}
}

func TestSSHReverseTunnelCreatesAuthenticatedUpstreamAndRevokes(t *testing.T) {
	testSSHReverseTunnel(t, false)
}

func TestSSHReverseTunnelFallsBackFromForcedWildcardToPrivateSocket(t *testing.T) {
	testSSHReverseTunnel(t, true)
}

func testSSHReverseTunnel(t *testing.T, private bool) {
	fakeSystemSSH(t)
	put(t, provider.Path(), `{"providers":[{"id":"api","name":"Fixture API","chat":"https://api.example.test/v1","key":"fixture-key","models":["model"]}]}`)
	// The fake SSH uses real TCP/Unix sockets and the real relay. This API
	// fixture simulates the old daemon's exact pre-write refusal once.
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Upstream
		if json.NewDecoder(r.Body).Decode(&in) != nil || r.Header.Get("X-Magpie-SSH-Target") != "fixture-context" {
			http.Error(w, "context", 409)
			return
		}
		if private && calls.Add(1) == 1 {
			body, _ := json.Marshal(map[string]string{"error": tunnelCheckError})
			json.NewEncoder(w).Encode(Response{Error: fmt.Sprintf("remote operation refused (400): %s", body)})
			return
		}
		client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
		defer client.CloseIdleConnections()
		var models struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		var ready bool
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(30 * time.Millisecond) {
			req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(in.Port)+"/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+in.Key)
			res, err := client.Do(req)
			if err != nil {
				continue
			}
			err = json.NewDecoder(res.Body).Decode(&models)
			res.Body.Close()
			if err == nil && res.StatusCode == 200 {
				ready = true
				break
			}
		}
		response := Response{}
		if !ready {
			response.Error = "reverse tunnel did not authenticate"
		} else {
			_, err := provider.AddSSHUpstream(in.ID, in.Name, "http://127.0.0.1:"+strconv.Itoa(in.Port), in.Key, in.Source, []string{models.Data[0].ID})
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Result, _ = json.Marshal(Tunnel{Provider: in.ID, Port: in.Port})
			}
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer remote.Close()
	t.Setenv("MAGPIE_SSH_FIXTURE_CONTROL", remote.URL)
	c, err := Connect(context.Background(), Host{ID: "12345678123456781234567812345678", Name: "fixture", Target: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	port, _ := strconv.Atoi(p)
	out, err := c.StartTunnel(context.Background(), port, func(keyID string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			who, ok := access.Authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !ok || who.KeyID != keyID {
				http.Error(w, "key", 401)
				return
			}
			io.WriteString(w, `{"data":[{"id":"api/model"}]}`)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := provider.Stored()
	if private && c.tunnel.forward.input == nil {
		t.Fatal("did not switch to a session-owned Unix transport")
	}
	if err != nil || len(ps) != 2 || ps[1].Key == "" || ps[1].ID != out.Provider {
		t.Fatal("upstream was not usable", err)
	}
	// An explicit retry returns the same provider, never a duplicate.
	again, err := c.StartTunnel(context.Background(), port, nil)
	if err != nil || again.Provider != out.Provider {
		t.Fatal(again, err)
	}
	if _, err := c.StartTunnel(context.Background(), port^1, nil); err == nil || !strings.Contains(err.Error(), "Reconnect SSH") {
		t.Fatalf("changed port silently reused the old tunnel: %v", err)
	}
	c.Close()
	if private {
		dir, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "private-forward-dir"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(string(dir)); !os.IsNotExist(err) {
			t.Fatal("private socket directory survived close", err)
		}
	}
	if _, ok := access.Authenticate(ps[1].Key); ok {
		t.Fatal("connection close left its key usable")
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+p, time.Second); err == nil {
		conn.Close()
		t.Fatal("reverse listener survived close")
	}
}

func TestSSHTunnelLostReplyKeepsPendingTransportWithoutFallback(t *testing.T) {
	fakeSystemSSH(t)
	put(t, provider.Path(), `{"providers":[{"id":"api","name":"Fixture API","chat":"https://api.example.test/v1","key":"fixture-key","models":["model"]}]}`)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":`)
	}))
	defer remote.Close()
	t.Setenv("MAGPIE_SSH_FIXTURE_CONTROL", remote.URL)
	c, err := Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err = c.StartTunnel(context.Background(), port, func(string) http.Handler { return http.NotFoundHandler() })
	if err == nil || !strings.Contains(err.Error(), "result unconfirmed") || c.tunnel == nil || c.tunnel.forward.input != nil {
		t.Fatalf("lost reply discarded or replaced its pending operation: %v", err)
	}
	if _, err = os.Stat(filepath.Join(os.Getenv("HOME"), "private-forward-dir")); !os.IsNotExist(err) {
		t.Fatal("lost reply started fallback")
	}
}

func TestSSHPrivateForwardPreservesHalfCloseAndCleansDirectory(t *testing.T) {
	fakeSystemSSH(t)
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		body, err := io.ReadAll(io.LimitReader(c, 64))
		if err == nil && string(body) == "request" {
			c.Write([]byte("complete response"))
		}
	}()
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	localPort := strconv.Itoa(target.Addr().(*net.TCPAddr).Port)
	f, err := startPrivateForward(context.Background(), Host{Name: "fixture", Target: "fixture"}, port, localPort)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("request"))
	c.(*net.TCPConn).CloseWrite()
	body, err := io.ReadAll(c)
	if err != nil || string(body) != "complete response" {
		t.Fatalf("half-close lost the reply: %q %v", body, err)
	}
	f.Close()
	dir, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "private-forward-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(dir)); !os.IsNotExist(err) {
		t.Fatal("private directory survived close", err)
	}
}

func TestSSHTunnelPrivateValidationRefusalReleasesKeyAndDirectory(t *testing.T) {
	fakeSystemSSH(t)
	put(t, provider.Path(), `{"providers":[{"id":"api","name":"Fixture API","chat":"https://api.example.test/v1","key":"fixture-key","models":["model"]}]}`)
	var calls atomic.Int32
	var key atomic.Value
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Upstream
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		key.Store(in.Key)
		calls.Add(1)
		body, _ := json.Marshal(map[string]string{"error": tunnelCheckError})
		json.NewEncoder(w).Encode(Response{Error: fmt.Sprintf("remote operation refused (400): %s", body)})
	}))
	defer remote.Close()
	t.Setenv("MAGPIE_SSH_FIXTURE_CONTROL", remote.URL)
	c, err := Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	_, err = c.StartTunnel(context.Background(), port, func(string) http.Handler { return http.NotFoundHandler() })
	if !tunnelCheckRefused(err) || calls.Load() != 2 || c.tunnel != nil || len(c.forwards) != 0 || len(c.closers) != 0 {
		t.Fatalf("confirmed private refusal kept pending resources: %v (%d calls)", err, calls.Load())
	}
	secret, _ := key.Load().(string)
	if secret == "" {
		t.Fatal("no allocated key was checked")
	}
	if _, ok := access.Authenticate(secret); ok {
		t.Fatal("refused private tunnel left its key usable")
	}
	dir, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "private-forward-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(dir)); !os.IsNotExist(err) {
		t.Fatal("refused private tunnel left its directory", err)
	}
}

func TestSSHPrivateForwardOccupiedPortLeavesNoDirectory(t *testing.T) {
	fakeSystemSSH(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	f, err := startPrivateForward(context.Background(), Host{Name: "fixture", Target: "fixture"}, occupied.Addr().(*net.TCPAddr).Port, "9")
	if f != nil {
		f.Close()
		t.Fatal("replaced an occupied listener")
	}
	var ssh *SSHError
	if !errors.As(err, &ssh) || ssh.Code != "forward_failed" {
		t.Fatalf("occupied port was not identified: %v", err)
	}
	dir, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "private-forward-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(dir)); !os.IsNotExist(err) {
		t.Fatal("failed start left a private directory", err)
	}
}

func TestSSHTunnelRejectsEmptyLocalCatalog(t *testing.T) {
	fakeSystemSSH(t)
	c, err := Connect(context.Background(), Host{Name: "fixture", Target: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	started := false
	_, err = c.StartTunnel(context.Background(), 3431, func(string) http.Handler {
		started = true
		return http.NotFoundHandler()
	})
	if err == nil || !strings.Contains(err.Error(), "The local gateway has no models") {
		t.Fatalf("empty local models did not give setup advice: %v", err)
	}
	if started || c.tunnel != nil || len(c.forwards) != 0 || len(c.closers) != 0 {
		t.Fatal("empty catalog started a tunnel or allocated a gateway key")
	}
	put(t, provider.Path(), "{broken")
	_, err = c.StartTunnel(context.Background(), 3431, func(string) http.Handler {
		started = true
		return http.NotFoundHandler()
	})
	if err == nil || strings.Contains(err.Error(), "has no models") || started {
		t.Fatalf("unreadable configuration was treated as an empty catalog: %v", err)
	}
}
