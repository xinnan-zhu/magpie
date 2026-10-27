// Package sshremote manages Magpie instances through the system OpenSSH.
// It owns connection descriptions and processes, never an agent's configuration.
package sshremote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
)

// Host uses OpenSSH's own configuration, including Include, Match and ProxyJump.
// Binary may select an existing installation; empty prefers our managed install, then searches PATH.
// No password, private key or web key is saved here or included in setup backups.
type Host struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target"`
	Port   int    `json:"port,omitempty"`
	Binary string `json:"binary,omitempty"`
}

var targetRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:\[\]-]*$`)

func (h Host) Validate() error {
	if !targetRE.MatchString(h.Target) || len(h.Target) > 255 || strings.Count(h.Target, "@") > 1 {
		return errors.New("use an SSH Host alias or user@host, without options or spaces")
	}
	if h.Port < 0 || h.Port > 65535 {
		return errors.New("SSH port must be between 1 and 65535")
	}
	if h.Binary != "" && (!strings.HasPrefix(h.Binary, "/") || strings.ContainsAny(h.Binary, "\x00\r\n")) {
		return errors.New("the remote binary must be an absolute path")
	}
	if strings.TrimSpace(h.Name) == "" || len(h.Name) > 120 {
		return errors.New("give this host a name of at most 120 characters")
	}
	return nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func HostsPath() string { return filepath.Join(appdir.Config(), "ssh-hosts.json") }

func Hosts() ([]Host, error) {
	b, err := os.ReadFile(HostsPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Host{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Host
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("SSH hosts: %w", err)
	}
	ids := map[string]bool{}
	for _, h := range out {
		if err = h.Validate(); err != nil {
			return nil, err
		}
		if h.ID == "" || ids[h.ID] {
			return nil, errors.New("invalid SSH host IDs")
		}
		ids[h.ID] = true
	}
	return out, nil
}

// SaveHost and DeleteHost serialize updates across windows and CLI processes.
func SaveHost(h Host) (Host, error) {
	if err := h.Validate(); err != nil {
		return h, err
	}
	unlock, held := filelock.Lock(HostsPath()+".lock", time.Second)
	defer unlock()
	if !held {
		return h, errors.New("SSH hosts are busy or cannot be locked")
	}
	hs, err := Hosts()
	if err != nil {
		return h, err
	}
	if h.ID == "" {
		h.ID = newID()
		hs = append(hs, h)
	} else {
		found := false
		for i := range hs {
			if hs[i].ID == h.ID {
				hs[i] = h
				found = true
				break
			}
		}
		if !found {
			return h, errors.New("unknown SSH host")
		}
	}
	return h, writeHosts(hs)
}
func DeleteHost(id string) error {
	unlock, held := filelock.Lock(HostsPath()+".lock", time.Second)
	defer unlock()
	if !held {
		return errors.New("SSH hosts are busy or cannot be locked")
	}
	hs, err := Hosts()
	if err != nil {
		return err
	}
	for i, h := range hs {
		if h.ID == id {
			return writeHosts(append(hs[:i], hs[i+1:]...))
		}
	}
	return errors.New("unknown SSH host")
}
func writeHosts(hs []Host) error {
	b, err := json.MarshalIndent(hs, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(HostsPath()), 0700); err != nil {
		return err
	}
	if _, err = os.Stat(HostsPath()); errors.Is(err, os.ErrNotExist) {
		if err = os.WriteFile(HostsPath(), nil, 0600); err != nil {
			return err
		}
	}
	if err = edit.WriteAtomic(HostsPath(), append(b, '\n')); err != nil {
		return err
	}
	return os.Chmod(HostsPath(), 0600)
}
