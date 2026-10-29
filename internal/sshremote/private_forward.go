package sshremote

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

//go:embed relay.py
var relaySource string

const tunnelCheckError = "SSH reverse forwarding did not authenticate on a loopback-only listener; check GatewayPorts and port conflicts"

// Old peers return this exact error only before AddSSHUpstream runs. A lost
// reply or provider write error must never trigger another transport attempt.
func tunnelCheckRefused(err error) bool {
	var remote *RemoteError
	return errors.As(err, &remote) && remote.Status == 400 && remote.Message == tunnelCheckError
}

const preparePrivateForward = `command -v python3 >/dev/null || { printf '%s\n' 'Python 3 is required for private SSH forwarding' >&2; exit 1; }; umask 077; mktemp -d /tmp/magpie-ssh.XXXXXXXXXX`

var privateDirectory = regexp.MustCompile(`^/tmp/magpie-ssh\.[A-Za-z0-9]{10}$`)

// startPrivateForward avoids servers forcing TCP reverse forwards onto all
// interfaces (GatewayPorts yes). SSH binds a private Unix socket; its command
// serves only loopback TCP and ends on the SSH channel's stdin EOF.
func startPrivateForward(ctx context.Context, h Host, port int, localPort string) (*forward, error) {
	prepare, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := proc.CommandContext(prepare, "ssh", append(BaseArgs(h, false), "--", h.Target, preparePrivateForward)...)
	out, stderr := &boundedBuffer{limit: 256}, &boundedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return nil, sshError(stderr.String(), err)
	}
	dir := strings.TrimSpace(out.String())
	if !privateDirectory.MatchString(dir) {
		return nil, errors.New("The remote shell returned an invalid private SSH directory")
	}
	remove := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Only this random, validated directory belongs to this attempt.
		cmd := proc.CommandContext(cleanup, "ssh", append(BaseArgs(h, false), "--", h.Target, "rm -rf -- "+quote(dir))...)
		_ = cmd.Run()
	}
	command := "python3 -u -c " + quote(relaySource) + " " + quote(dir) + " " + strconv.Itoa(port)
	f, stdout, err := launchForward(ctx, h, "-R"+dir+"/gateway.sock:127.0.0.1:"+localPort, command)
	if err != nil {
		remove()
		return nil, err
	}
	f.onClose = remove
	ready := make(chan error, 1)
	go func() {
		var reply struct {
			Ready bool `json:"ready"`
			Port  int  `json:"port"`
		}
		line, err := bufio.NewReader(io.LimitReader(stdout, 1024)).ReadString('\n')
		if err == nil {
			err = json.Unmarshal([]byte(line), &reply)
		}
		if err == nil && (!reply.Ready || reply.Port != port) {
			err = errors.New("The private SSH listener returned an invalid ready reply")
		}
		ready <- err
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case err = <-ready:
	case <-prepare.Done():
		err = prepare.Err()
	}
	if err != nil {
		f.Close()
		if cause := f.Error(); cause != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			err = cause
		}
		return nil, err
	}
	return f, nil
}
