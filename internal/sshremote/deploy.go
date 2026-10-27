package sshremote

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yetone/magpie/internal/proc"
)

// Deploy uploads a verified Linux nogui binary, without initializing it or
// replacing a PATH installation. The previous uploaded binary is retained.
// The digest comes from the official update feed or an explicit offline choice.
func Deploy(ctx context.Context, h Host, s Inspection, path, digest string) error {
	if h.Binary != "" {
		return errors.New("clear the explicit binary path before deploying a managed installation")
	}
	if err := h.Validate(); err != nil {
		return err
	}
	if len(digest) != 64 {
		return errors.New("a SHA-256 digest is required")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return err
	}
	osName, arch := strings.ToLower(s.Context.OS), s.Context.Arch
	if osName != "linux" {
		return errors.New("SSH deployment supports Linux only")
	}
	want := elf.EM_NONE
	switch arch {
	case "amd64", "x86_64":
		want = elf.EM_X86_64
	case "arm64", "aarch64":
		want = elf.EM_AARCH64
	default:
		return errors.New("unsupported remote architecture")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	e, err := elf.NewFile(f)
	if err != nil {
		return errors.New("choose a Linux Magpie nogui binary")
	}
	if e.Machine != want {
		return errors.New("the binary architecture does not match this remote host")
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), digest) {
		return errors.New("the binary SHA-256 does not match")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	script := `set -eu; umask 077; d="$HOME/.local/lib/magpie/ssh"; mkdir -p "$d"; if [ -e "$d/data" ]; then exit 1; fi; mkdir "$d/deploy.lock"; t=$(mktemp "$d/upload.XXXXXXXX"); trap 'rm -f "$t"; rmdir "$d/deploy.lock"' EXIT HUP INT TERM; cat > "$t"; printf '%s  %s\n' ` + quote(strings.ToLower(digest)) + ` "$t" | sha256sum -c - >/dev/null; chmod 700 "$t"; if [ -f "$d/magpie" ]; then cp -p "$d/magpie" "$d/magpie.previous"; fi; mv -f "$t" "$d/magpie"`
	args := append(BaseArgs(h, false), "--", h.Target, script)
	cmd := proc.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = f
	stderr := &boundedBuffer{limit: 8192}
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		return sshError(stderr.String(), err)
	}
	return nil
}

func ArtifactName(s Inspection) (string, error) {
	if strings.ToLower(s.Context.OS) != "linux" {
		return "", errors.New("SSH deployment supports Linux only")
	}
	arch := s.Context.Arch
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	if arch != "amd64" && arch != "arm64" {
		return "", errors.New("unsupported remote architecture")
	}
	return fmt.Sprintf("magpie-cli-linux-%s", arch), nil
}
