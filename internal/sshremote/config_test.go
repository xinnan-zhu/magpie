package sshremote

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSSHConfigHosts(t *testing.T) {
	home := sshHome(t)
	base := filepath.Join(home, ".ssh")
	// A directory in place of a key fails if discovery opens IdentityFile.
	if err := os.MkdirAll(filepath.Join(base, "private-key"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(base, "config"), `# local fixture, never connect
Host "work" work-alt *.example.test !excluded
  HostName example.test
  User fixture
  Port 2222
  IdentityFile ~/.ssh/private-key
  ProxyJump bastion
Include "conf.d/*.conf" missing
Host=work after-include # work appears only once
Match exec "must-never-execute"
  Include ~/extra-config
Host -oProxyCommand=evil user@host
`)
	put(t, filepath.Join(base, "conf.d", "10.conf"), "hOsT = included\nInclude config\n")
	put(t, filepath.Join(base, "conf.d", "20.conf"), "Host included-last\n")
	put(t, filepath.Join(home, "extra-config"), "Host conditional\n")
	// Even a subprocess launched without connecting would hit this fixture.
	put(t, filepath.Join(home, "bin", "ssh"), "#!/bin/sh\nexit 91\n")
	t.Setenv("PATH", filepath.Join(home, "bin"))
	hosts, err := ConfigHosts()
	want := []string{"work", "work-alt", "included", "included-last", "after-include", "conditional"}
	if err != nil || !reflect.DeepEqual(hosts, want) {
		t.Fatalf("aliases = %v, %v; want %v", hosts, err, want)
	}
	b, err := os.ReadFile(filepath.Join(base, "config"))
	if err != nil || !strings.Contains(string(b), "IdentityFile ~/.ssh/private-key") {
		t.Fatal("discovery changed the SSH configuration", err)
	}
}

func TestSSHConfigHostsEmptyAndUnreadable(t *testing.T) {
	home := sshHome(t)
	hosts, err := ConfigHosts()
	if err != nil || hosts == nil || len(hosts) != 0 {
		t.Fatal("missing config", hosts, err)
	}
	config := filepath.Join(home, ".ssh", "config")
	for _, body := range []string{
		"Host known\nHost \"broken\n",
		"Host known\nInclude [bad\n",
		"Host known\nInclude %h.conf\n",
		"Host known\nInclude directory\n",
		"Host " + strings.Repeat("a", 5<<20),
	} {
		put(t, config, body)
		if err := os.MkdirAll(filepath.Join(home, ".ssh", "directory"), 0700); err != nil {
			t.Fatal(err)
		}
		if hosts, err := ConfigHosts(); err == nil || hosts != nil {
			t.Fatalf("unreadable config returned %v, %v", hosts, err)
		}
	}
	put(t, config, "Host * !excluded *.test\n")
	if hosts, err := ConfigHosts(); err != nil || len(hosts) != 0 {
		t.Fatal("wildcard config", hosts, err)
	}
}
