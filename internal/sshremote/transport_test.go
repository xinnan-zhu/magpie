package sshremote

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
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
	for n, a := range args {
		if a != "-L" && a != "-R" {
			continue
		}
		spec := strings.Split(args[n+1], ":")
		if len(spec) != 4 {
			return 2
		}
		ln, e := net.Listen("tcp", spec[0]+":"+spec[1])
		if e != nil {
			return 3
		}
		for {
			c, e := ln.Accept()
			if e != nil {
				return 0
			}
			go func() {
				defer c.Close()
				other, e := net.Dial("tcp", spec[2]+":"+spec[3])
				if e != nil {
					return
				}
				defer other.Close()
				done := make(chan struct{})
				go func() { io.Copy(c, other); close(done) }()
				io.Copy(other, c)
				other.Close()
				<-done
			}()
		}
	}
	var req Request
	if e := json.NewDecoder(os.Stdin).Decode(&req); e != nil {
		return 4
	}
	var out any
	switch req.Method {
	case "inspect":
		out = Inspection{Protocol: Protocol, Installed: true, Compatible: true, Ready: true, Context: Context{Target: "fixture-context"}}
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
func TestDeployChecksDigestAndKeepsPrevious(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX remote upload")
	}
	home := sshHome(t)
	bin := filepath.Join(home, "bin")
	put(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nfor arg do last=$arg; done\nexec /bin/sh -c \"$last\"\n")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	if runtime.GOOS == "darwin" {
		put(t, filepath.Join(bin, "sha256sum"), "#!/bin/sh\nexec /usr/bin/shasum -a 256 \"$@\"\n")
	}
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], 2)
	binary.LittleEndian.PutUint16(data[18:], 62)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint16(data[52:], 64)
	path := filepath.Join(home, "artifact")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	h := Host{Name: "fixture", Target: "fixture"}
	s := Inspection{Context: Context{OS: "linux", Arch: "amd64"}}
	if e := Deploy(context.Background(), h, s, path, strings.Repeat("0", 64)); e == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, e := os.Stat(filepath.Join(home, ".local/lib/magpie/ssh/magpie")); !os.IsNotExist(e) {
		t.Fatal("upload ran before validation", e)
	}
	if e := Deploy(context.Background(), h, s, path, digest); e != nil {
		t.Fatal(e)
	}
	data = append(data, 'n', 'e', 'w')
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	sum = sha256.Sum256(data)
	digest = hex.EncodeToString(sum[:])
	if e := Deploy(context.Background(), h, s, path, digest); e != nil {
		t.Fatal(e)
	}
	old, e := os.ReadFile(filepath.Join(home, ".local/lib/magpie/ssh/magpie.previous"))
	if e != nil || len(old) != 64 {
		t.Fatal("previous installation lost", len(old), e)
	}
	st, e := os.Stat(filepath.Join(home, ".local/lib/magpie/ssh/magpie"))
	if e != nil || st.Mode().Perm() != 0700 {
		t.Fatal(st, e)
	}
	if e := Deploy(context.Background(), h, Inspection{Context: Context{OS: "linux", Arch: "arm64"}}, path, digest); e == nil {
		t.Fatal("wrong architecture accepted")
	}
}

func TestSSHReverseTunnelCreatesAuthenticatedUpstreamAndRevokes(t *testing.T) {
	fakeSystemSSH(t)
	// The fake SSH forwards TCP only. This local management fixture stands in
	// for the remote daemon; Linux socket-table scope is tested separately.
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Upstream
		if json.NewDecoder(r.Body).Decode(&in) != nil || r.Header.Get("X-Magpie-SSH-Target") != "fixture-context" {
			http.Error(w, "context", 409)
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
	if err != nil || len(ps) != 1 || ps[0].Key == "" || ps[0].ID != out.Provider {
		t.Fatal("upstream was not usable", err)
	}
	// An explicit retry returns the same provider, never a duplicate.
	again, err := c.StartTunnel(context.Background(), port, nil)
	if err != nil || again.Provider != out.Provider {
		t.Fatal(again, err)
	}
	c.Close()
	if _, ok := access.Authenticate(ps[0].Key); ok {
		t.Fatal("connection close left its key usable")
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+p, time.Second); err == nil {
		conn.Close()
		t.Fatal("reverse listener survived close")
	}
}
