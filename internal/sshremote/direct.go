package sshremote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/provider"
)

// Direct starts one remote agent session on the local gateway's reverse
// tunnel. It uses per-process options, never rewrites agent files or copies a
// subscription login. The normal remote gateway connection can coexist.
func Direct(ctx context.Context, id, agent, model string, args []string) error {
	if agent != "claude" && agent != "codex" {
		return errors.New("choose claude or codex")
	}
	ps, err := provider.Stored()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.ID != id {
			continue
		}
		if !strings.HasPrefix(id, "ssh-") || p.SSHSource == "" || !p.IsRemoteMagpie() || !strings.HasPrefix(p.Anthropic, "http://127.0.0.1:") {
			return errors.New("choose an SSH upstream provider")
		}
		if p.Off || p.Key == "" {
			return errors.New("the SSH upstream is disabled or has no key")
		}
		if model == "" || strings.HasPrefix(model, "-") {
			return errors.New("specify a model id listed by the local gateway")
		}
		env := os.Environ()
		set := func(key, value string) {
			prefix := key + "="
			out := env[:0]
			for _, v := range env {
				if !strings.HasPrefix(v, prefix) {
					out = append(out, v)
				}
			}
			env = append(out, prefix+value)
		}
		var opts []string
		if agent == "claude" {
			for _, name := range agentenv.ClaudeProviders {
				set(name, "")
			}
			set("ANTHROPIC_BASE_URL", p.Anthropic)
			set("ANTHROPIC_AUTH_TOKEN", p.Key)
			set("ANTHROPIC_API_KEY", p.Key)
			opts = []string{"--model", model}
		} else {
			set("OPENAI_API_KEY", p.Key)
			q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
			// A fresh table cannot inherit bearer tokens or headers from a
			// provider the user already has in their Codex configuration.
			var nonce [8]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return err
			}
			name := "magpie_ssh_" + hex.EncodeToString(nonce[:])
			prefix := "model_providers." + name
			opts = []string{"-m", model, "-c", "model_provider=" + q(name), "-c", prefix + `.name="Magpie SSH"`, "-c", prefix + ".base_url=" + q(p.Responses), "-c", prefix + `.env_key="OPENAI_API_KEY"`, "-c", prefix + `.wire_api="responses"`, "-c", prefix + `.requires_openai_auth=false`}
		}
		cmd := proc.CommandContext(ctx, agent, append(opts, args...)...)
		cmd.Env = env
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return errors.New("SSH upstream was not found; start its local gateway tunnel first")
}
