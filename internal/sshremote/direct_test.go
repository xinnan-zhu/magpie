package sshremote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/provider"
)

func TestSSHDirectUsesProcessOptionsWithoutAgentWrites(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX agent fixture")
	}
	home := sshHome(t)
	const id = "ssh-12345678123456781234567812345678"
	if _, err := provider.AddSSHUpstream(id, "Fixture", "http://127.0.0.1:3431", "fixture-direct-secret", "fixture-source", []string{"api/model"}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	argsFile, envFile := filepath.Join(home, "args"), filepath.Join(home, "env")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + quote(argsFile) + "\nprintf '%s\\n' \"$ANTHROPIC_BASE_URL\" \"$ANTHROPIC_AUTH_TOKEN\" \"$OPENAI_API_KEY\" \"$CLAUDE_CODE_USE_BEDROCK\" > " + quote(envFile) + "\n"
	for _, agent := range []string{"claude", "codex"} {
		put(t, filepath.Join(bin, agent), script)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	for _, name := range agentenv.ClaudeProviders {
		t.Setenv(name, "1")
	}
	config := filepath.Join(home, ".codex/config.toml")
	put(t, config, "# user configuration\n")
	for _, agent := range []string{"claude", "codex"} {
		if err := Direct(context.Background(), id, agent, "api/model", []string{"fixture prompt"}); err != nil {
			t.Fatal(err)
		}
		args, _ := os.ReadFile(argsFile)
		env, _ := os.ReadFile(envFile)
		if strings.Contains(string(args), "fixture-direct-secret") || !strings.Contains(string(args), "api/model") || !strings.Contains(string(env), "fixture-direct-secret") {
			t.Fatal("missing process authentication or secret in arguments")
		}
		if agent == "claude" && strings.Contains(string(env), "\n1\n") {
			t.Fatal("inherited Claude provider switch")
		}
		if agent == "codex" && (!strings.Contains(string(args), "model_provider=\"magpie_ssh_") || !strings.Contains(string(args), "requires_openai_auth=false")) {
			t.Fatal("missing isolated Codex provider", string(args))
		}
	}
	b, _ := os.ReadFile(config)
	if string(b) != "# user configuration\n" {
		t.Fatal("changed agent configuration")
	}
}
