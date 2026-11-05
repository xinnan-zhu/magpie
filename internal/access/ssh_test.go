package access

import (
	"testing"
	"time"
)

func TestSSHMachineKeysStayOnTheirComputer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	machine, err := NewMachineKey("SSH fixture")
	if err != nil {
		t.Fatal(err)
	}
	if who, ok := Authenticate(machine.Secret); !ok || who.KeyID != machine.ID || !who.Machine {
		t.Fatal(who, ok)
	}
	exported, err := Export()
	if err != nil || len(exported) != 0 {
		t.Fatal("exported machine credential", exported, err)
	}
	if err = Restore([]Key{{ID: "incoming-machine", Machine: true, Secret: "foreign"}, {ID: "normal", Secret: "normal-secret"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Authenticate("foreign"); ok {
		t.Fatal("restored another machine's credential")
	}
	if _, ok := Authenticate(machine.Secret); !ok {
		t.Fatal("restore removed own tunnel key")
	}
	if _, err = Update("off-key", Change{Key: machine.ID}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Authenticate(machine.Secret); ok {
		t.Fatal("revoked SSH key still authenticates")
	}
}

// An SSH connection key must not mint credentials for the ordinary listener.
func TestSSHMachineKeyCannotIssue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	machine, err := NewMachineKey("SSH fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update("issuer-key", Change{Key: machine.ID, Issues: true}); err == nil {
		t.Error("an SSH machine key was allowed to issue keys")
	}
	// Even a store edited by hand cannot lift this transport boundary.
	keys, err := load()
	if err != nil {
		t.Fatal(err)
	}
	keys[0].Issues = true
	keys = append(keys, Key{ID: "child", Secret: "child-secret", Issuer: machine.ID, Expires: Now().Add(time.Hour)})
	if err := save(keys); err != nil {
		t.Fatal(err)
	}
	if who, ok := Authenticate(machine.Secret); !ok || who.Issues {
		t.Error("an SSH machine key advertises issuance", who, ok)
	}
	if _, err := IssueKey(Issue{TTL: time.Hour, Issuer: machine.ID}); err == nil {
		t.Error("issued an unrestricted child under an SSH key")
	}
	if _, ok := Authenticate("child-secret"); ok || On("child") {
		t.Error("a key issued under an SSH key authenticates outside its transport")
	}
}
