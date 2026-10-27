package access

import "testing"

func TestSSHMachineKeysStayOnTheirComputer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	machine, err := NewMachineKey("SSH fixture")
	if err != nil {
		t.Fatal(err)
	}
	if who, ok := Authenticate(machine.Secret); !ok || who.KeyID != machine.ID {
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
