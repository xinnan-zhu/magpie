package backup

import (
	"github.com/yetone/magpie/internal/provider"
	"testing"
)

func TestSSHUpstreamCredentialNeverEntersBackup(t *testing.T) {
	home(t)
	const id = "ssh-12345678123456781234567812345678"
	if _, err := provider.AddSSHUpstream(id, "Fixture", "http://127.0.0.1:3431", "fixture-tunnel-secret", "fixture-source", []string{"api/model"}); err != nil {
		t.Fatal(err)
	}
	b, err := Collect(true, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range b.Providers {
		if p.ID == id {
			if p.Key != "" {
				t.Fatal("backup carried a machine credential")
			}
			return
		}
	}
	t.Fatal("backup lost upstream model picks")
}
