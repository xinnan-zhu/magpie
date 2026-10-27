package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yetone/magpie/internal/sshremote"
)

const sshUsage = `magpie ssh — manage remote Magpie through system OpenSSH

  magpie ssh hosts
  magpie ssh add <name> <user@host-or-alias>
  magpie ssh remove <id>
  magpie ssh inspect <id-or-alias>
  magpie ssh install <id-or-alias> <linux-binary> <sha256>
  magpie ssh service <id-or-alias> install|start
  magpie ssh web <id-or-alias>     print a one-use login link; keep this command running

Authenticate and verify host keys with system SSH in a terminal first.
install uploads a verified nogui binary; it does not start or initialize Magpie.
service install explicitly installs and starts the full user service with a
loopback Web page. Existing services are preserved. No sudo or LAN changes.
`

func sshCmd(args []string) error {
	if len(args) == 0 || helpAsked(args) || args[0] == "help" {
		fmt.Print(sshUsage)
		return nil
	}
	emit := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
	hs, err := sshremote.Hosts()
	if err != nil {
		return err
	}
	switch args[0] {
	case "hosts":
		if len(args) != 1 {
			return errors.New(sshUsage)
		}
		return emit(hs)
	case "add":
		if len(args) != 3 {
			return errors.New(sshUsage)
		}
		h, err := sshremote.SaveHost(sshremote.Host{Name: args[1], Target: args[2]})
		if err != nil {
			return err
		}
		return emit(h)
	case "remove":
		if len(args) != 2 {
			return errors.New(sshUsage)
		}
		return sshremote.DeleteHost(args[1])
	}
	if len(args) < 2 {
		return errors.New(sshUsage)
	}
	h := sshremote.Host{Name: args[1], Target: args[1]}
	for _, saved := range hs {
		if saved.ID == args[1] {
			h = saved
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return errors.New(sshUsage)
		}
		var s sshremote.Inspection
		if err = sshremote.Call(ctx, h, sshremote.Request{Method: "inspect"}, &s); err != nil {
			return err
		}
		return emit(s)
	case "install":
		if len(args) != 4 {
			return errors.New(sshUsage)
		}
		var s sshremote.Inspection
		if err = sshremote.Call(ctx, h, sshremote.Request{Method: "inspect"}, &s); err != nil {
			return err
		}
		return sshremote.Deploy(ctx, h, s, args[2], args[3])
	case "service":
		if len(args) != 3 || args[2] != "install" && args[2] != "start" {
			return errors.New(sshUsage)
		}
		var s sshremote.Inspection
		if err = sshremote.Call(ctx, h, sshremote.Request{Method: "inspect"}, &s); err != nil {
			return err
		}
		target := s.Context.Target
		if s.Shell != nil {
			target = s.Shell.Target
		}
		if err = sshremote.Call(ctx, h, sshremote.Request{Method: "service-" + args[2], Target: target}, &s); err != nil {
			return err
		}
		return emit(s)
	case "web":
		if len(args) != 2 {
			return errors.New(sshUsage)
		}
		c, err := sshremote.Connect(ctx, h)
		if err != nil {
			return err
		}
		defer c.Close()
		link, err := c.OpenWeb(ctx)
		if err != nil {
			return err
		}
		fmt.Println(link)
		wait, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-wait.Done()
		return nil
	}
	return errors.New(sshUsage)
}
