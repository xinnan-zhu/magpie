package sshremote

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
)

// ConfigHosts lists concrete aliases, not resolved connection settings. Reading
// only Host and Include avoids executing Match exec or opening IdentityFile.
// OpenSSH still evaluates the complete configuration when the user connects.
func ConfigHosts() ([]string, error) {
	home, err := appdir.Home()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(home, ".ssh")
	hosts := []string{}
	files, aliases := map[string]bool{}, map[string]bool{}
	remaining := int64(4 << 20)
	var read func(string, int) error
	read = func(path string, depth int) error {
		path = filepath.Clean(path)
		if files[path] {
			return nil
		}
		if depth > 16 || len(files) >= 128 {
			return errors.New("SSH configuration has too many includes")
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > remaining {
			return errors.New("cannot read SSH configuration")
		}
		f, err := os.Open(path)
		if err != nil {
			return errors.New("cannot read SSH configuration")
		}
		b, err := io.ReadAll(io.LimitReader(f, remaining+1))
		f.Close()
		remaining -= int64(len(b))
		if err != nil || remaining < 0 {
			return errors.New("SSH configuration is unreadable or too large")
		}
		files[path] = true
		s := bufio.NewScanner(bytes.NewReader(b))
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			key, rest, _ := strings.Cut(line, "=")
			if i := strings.IndexAny(line, " \t"); i >= 0 && (len(key) == len(line) || i < len(key)) {
				key, rest = line[:i], strings.TrimLeft(line[i:], " \t=")
			}
			key = strings.ToLower(strings.TrimSpace(key))
			if key != "host" && key != "include" {
				continue
			}
			values, err := configWords(rest)
			if err != nil || len(values) == 0 {
				return errors.New("cannot parse SSH Host or Include")
			}
			for _, value := range values {
				if key == "host" {
					if strings.ContainsAny(value, "*?!@") || (Host{Name: "SSH", Target: value}).Validate() != nil || aliases[value] {
						continue
					}
					aliases[value] = true
					hosts = append(hosts, value)
					continue
				}
				pattern, err := configInclude(value, home, base)
				if err != nil {
					return err
				}
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return errors.New("invalid SSH Include pattern")
				}
				for _, match := range matches {
					if err := read(match, depth+1); err != nil {
						return err
					}
				}
			}
		}
		if s.Err() != nil {
			return errors.New("SSH configuration is unreadable or too large")
		}
		return nil
	}
	if err := read(filepath.Join(base, "config"), 0); err != nil {
		return nil, err
	}
	return hosts, nil
}

// OpenSSH permits double-quoted arguments, escaped quotes and inline comments.
func configWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	quoted, started := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line) && line[i+1] == '"':
			i++
			word.WriteByte('"')
			started = true
		case c == '"':
			quoted = !quoted
			started = true
		case !quoted && c == '#':
			line = line[:i]
		case !quoted && (c == ' ' || c == '\t' || c == '\r'):
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quoted {
		return nil, errors.New("unterminated SSH argument")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}

func configInclude(pattern, home, base string) (string, error) {
	var missing bool
	pattern = os.Expand(pattern, func(key string) string {
		value, ok := os.LookupEnv(key)
		missing = missing || !ok
		return value
	})
	if missing || strings.Contains(pattern, "%") || (strings.HasPrefix(pattern, "~") && !strings.HasPrefix(pattern, "~/")) {
		return "", errors.New("SSH Include cannot be listed automatically; enter a destination manually")
	}
	if strings.HasPrefix(pattern, "~/") {
		pattern = filepath.Join(home, pattern[2:])
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(base, pattern)
	}
	return pattern, nil
}
