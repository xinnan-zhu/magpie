package sshremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
)

type Tunnel struct {
	Provider string `json:"provider"`
	Port     int    `json:"port"`
	input    Upstream
	forward  *forward
}
type Upstream struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Key    string `json:"key"`
}

// StartTunnel shares only the explicitly authenticated ingress the caller gives
// it. The forward and the key are disposed with this connection. It never
// changes LAN settings or installs a fallback.
func (c *Connection) StartTunnel(ctx context.Context, port int, front func(string) http.Handler) (Tunnel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return Tunnel{}, errors.New("SSH connection is closed")
	}
	if c.tunnel != nil {
		if err := c.tunnel.forward.Error(); err != nil {
			return Tunnel{}, err
		}
		var result Tunnel
		err := c.API(ctx, "/api/ssh/upstream", c.tunnel.input, &result)
		return result, err
	}
	if !c.Inspection.Ready || port < 1024 || port > 65535 {
		return Tunnel{}, errors.New("choose a free remote loopback port between 1024 and 65535")
	}
	k, err := access.NewMachineKey("SSH · " + c.Host.Name)
	if err != nil {
		return Tunnel{}, err
	}
	revoke := func() { access.Update("remove-key", access.Change{Key: k.ID}) }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		revoke()
		return Tunnel{}, err
	}
	server := &http.Server{Handler: front(k.ID), ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(ln)
	cleanup := func() { server.Close(); revoke() }
	_, lp, _ := net.SplitHostPort(ln.Addr().String())
	f, err := startForward(c.ctx, c.Host, "-R127.0.0.1:"+strconv.Itoa(port)+":127.0.0.1:"+lp)
	if err != nil {
		cleanup()
		return Tunnel{}, err
	}
	// Keep the operation even if the reply is lost: retrying the same id is a
	// create-only operation, never a second provider or a replayed model request.
	c.forwards = append(c.forwards, f)
	c.closers = append(c.closers, cleanup)
	local, err := ReadContext("")
	if err != nil {
		return Tunnel{}, err
	}
	id := c.Host.ID
	if id == "" {
		id = c.ID
	}
	in := Upstream{Source: local.Target + ":" + id, ID: "ssh-" + id, Name: "SSH · " + c.Host.Name, Port: port, Key: k.Secret}
	c.tunnel = &Tunnel{Provider: in.ID, Port: port, input: in, forward: f}
	var result Tunnel
	if err = c.API(ctx, "/api/ssh/upstream", in, &result); err != nil {
		return Tunnel{}, err
	}
	if err = f.Error(); err != nil {
		return Tunnel{}, err
	}
	return result, nil
}

// CheckUpstream confirms both the bind scope and authenticated HTTP path.
// A running ssh process alone does not mean a reverse forward is ready.
func CheckUpstream(ctx context.Context, in Upstream) (Tunnel, error) {
	if in.Port < 1024 || in.Port > 65535 || in.Key == "" {
		return Tunnel{}, errors.New("invalid SSH upstream")
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	until, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	base := "http://127.0.0.1:" + strconv.Itoa(in.Port)
	for {
		if err := loopbackListener(in.Port); err == nil {
			req, _ := http.NewRequestWithContext(until, "GET", base+"/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+in.Key)
			res, err := client.Do(req)
			if err == nil {
				err = json.NewDecoder(http.MaxBytesReader(nil, res.Body, MaxMessage)).Decode(&models)
				res.Body.Close()
				if err == nil && res.StatusCode == 200 {
					break
				}
			}
		}
		select {
		case <-until.Done():
			return Tunnel{}, errors.New("SSH reverse forwarding did not authenticate on a loopback-only listener; check GatewayPorts and port conflicts")
		case <-time.After(60 * time.Millisecond):
		}
	}
	ids := []string{}
	for _, m := range models.Data {
		ids = append(ids, m.ID)
	}
	id, err := provider.AddSSHUpstream(in.ID, in.Name, base, in.Key, in.Source, ids)
	return Tunnel{Provider: id, Port: in.Port}, err
}
func loopbackListener(port int) error {
	found := false
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) && strings.HasSuffix(path, "6") {
			continue
		}
		if err != nil {
			return err
		}
		got, err := checkListeners(string(b), port)
		if err != nil {
			return err
		}
		found = found || got
	}
	if !found {
		return errors.New("reverse listener not found")
	}
	return nil
}
func checkListeners(table string, port int) (bool, error) {
	found := false
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		host, p, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(p, 16, 32)
		if err != nil {
			return false, err
		}
		if int(n) != port {
			continue
		}
		if host != "0100007F" && host != "00000000000000000000000001000000" {
			return false, fmt.Errorf("port %d is not loopback-only", port)
		}
		found = true
	}
	return found, nil
}
