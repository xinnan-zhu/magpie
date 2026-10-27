package provider

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSSHCopySelectionAndAtomicMerge(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	source := file{Providers: []Provider{{ID: "api", Name: "API", Chat: "https://api.example/v1", Key: "source-secret", Proxy: "http://local-proxy:80", Headers: map[string]string{"Authorization": "Bearer custom-secret", "X-Title": "kept"}, Models: []string{"model"}, Fallback: []string{"codex/gpt"}}, {ID: "claude", Name: "Subscription"}}, Groups: []Group{{ID: "work", Name: "Work", Members: []string{"api/model"}, Pick: "api/model", Routing: Manual}}}
	if err := store(source); err != nil {
		t.Fatal(err)
	}
	selection, err := SelectCopy([]string{"api"}, []string{"work"}, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(selection)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "proxy") || strings.Contains(string(b), "codex") {
		t.Fatalf("nonportable data left: %s", b)
	}
	if _, err = SelectCopy([]string{"claude"}, nil, true); err == nil {
		t.Fatal("copied subscription")
	}
	withKeys, err := SelectCopy([]string{"api"}, nil, true)
	if err != nil || withKeys.Providers[0].Key != "source-secret" {
		t.Fatal(withKeys, err)
	}
	if err = store(file{Providers: []Provider{{ID: "api", Name: "Existing", Key: "remote-secret"}, {ID: "api-ssh", Name: "Collision", Key: "other-secret"}}}); err != nil {
		t.Fatal(err)
	}
	const id = "12345678123456781234567812345678"
	plan, err := PlanImport(id, selection)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Receipt.IDs["api"] != "api-ssh-2" || plan.Setup.Groups[0].Members[0] != "api-ssh-2/model" {
		t.Fatalf("references not mapped: %+v", plan)
	}
	receipt, err := ApplyImport(plan)
	if err != nil {
		t.Fatal(err)
	}
	// Lost reply: retrying the same operation returns its receipt, no duplicates.
	again, err := ApplyImport(plan)
	if err != nil || again.Digest != receipt.Digest {
		t.Fatal(again, err)
	}
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Providers) != 3 || f.Providers[0].Key != "remote-secret" || f.Providers[1].Key != "other-secret" || f.Providers[2].Key != "" {
		t.Fatalf("overwrote existing credentials: %+v", f.Providers)
	}
	if _, ok, err := ImportStatus(id); err != nil || !ok {
		t.Fatal(ok, err)
	}
}
func TestSSHImportRefusesStaleAndFailedReads(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	plan, err := PlanImport("12345678123456781234567812345678", SetupCopy{Providers: []Provider{{ID: "api", Name: "API", Chat: "https://example.com/v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store(file{Providers: []Provider{{ID: "newer", Name: "Newer"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(Path())
	if _, err = ApplyImport(plan); err == nil {
		t.Fatal("accepted stale preview")
	}
	after, _ := os.ReadFile(Path())
	if string(before) != string(after) {
		t.Fatal("stale apply changed file")
	}
	stale, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if err = store(file{Providers: []Provider{{ID: "latest", Name: "Latest"}}}); err != nil {
		t.Fatal(err)
	}
	stale.Providers = append(stale.Providers, Provider{ID: "late", Name: "Late"})
	if err = store(stale); err == nil {
		t.Fatal("a stale writer replaced a concurrent change")
	}
	if err = os.WriteFile(Path(), []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	os.Remove(Path() + ".bak")
	if _, err = PlanImport(plan.ID, plan.Setup); err == nil {
		t.Fatal("broken file read as empty")
	}
	if err = os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(Path(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = PlanImport(plan.ID, plan.Setup); err == nil {
		t.Fatal("unreadable file read as empty")
	}
}
func TestSSHCopyRequiresExplicitDependencies(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	setup := SetupCopy{Providers: []Provider{{ID: "api", Name: "API", Chat: "https://example.com/v1"}}, Groups: []Group{{ID: "work", Members: []string{"omitted/model"}}}}
	if _, err := PlanImport("12345678123456781234567812345678", setup); err == nil {
		t.Fatal("matched a dependency against unrelated remote configuration")
	}
	for _, base := range []string{"http://127.0.0.1:1234/v1", "http://localhost/v1", "https://user:secret@example.com/v1", "https://example.com/v1?key=secret"} {
		if _, err := portableProvider(Provider{ID: "api", Chat: base}, true); err == nil {
			t.Fatal("copied endpoint", base)
		}
	}
}

func TestSSHUpstreamReconnectKeepsModelPicks(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const id = "ssh-12345678123456781234567812345678"
	if _, err := AddSSHUpstream(id, "Laptop", "http://127.0.0.1:3431", "old-key", "source", []string{"api/model"}); err != nil {
		t.Fatal(err)
	}
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if f.Providers[0].Key != "old-key" {
		t.Fatal("initial upstream has no key")
	}
	f.Providers[0].Models = []string{"api/selected"}
	if err = store(f); err != nil {
		t.Fatal(err)
	}
	if _, err = AddSSHUpstream(id, "Laptop", "http://127.0.0.1:3431", "new-key", "source", []string{"api/model"}); err != nil {
		t.Fatal(err)
	}
	ps, err := Stored()
	if err != nil || len(ps) != 1 || ps[0].Key != "new-key" || ps[0].Models[0] != "api/selected" {
		t.Fatal(ps, err)
	}
	if _, err = AddSSHUpstream(id, "Other", "http://127.0.0.1:3431", "attacker-key", "other-source", nil); err == nil {
		t.Fatal("replaced another source's upstream")
	}
	f, err = read()
	if err != nil {
		t.Fatal(err)
	}
	f.Providers[0].Chat = "https://user.example/v1"
	if err = store(f); err != nil {
		t.Fatal(err)
	}
	if _, err = AddSSHUpstream(id, "Laptop", "http://127.0.0.1:3431", "third-key", "source", nil); err == nil {
		t.Fatal("overwrote user's edited endpoints")
	}
}

func TestSSHImportRejectsInjectedDependenciesAndDuplicates(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := SetupCopy{Providers: []Provider{{ID: "api", Name: "API", Chat: "https://api.example/v1"}}, Groups: []Group{{ID: "work", Name: "Work", Members: []string{"api/model:high"}}}}
	for _, change := range []func(*ImportPlan){
		func(p *ImportPlan) { p.Setup.Providers = append(p.Setup.Providers, p.Setup.Providers[0]) },
		func(p *ImportPlan) { p.Setup.Groups[0].Members = []string{"existing/model"} },
		func(p *ImportPlan) { p.Setup.Groups[0].Members = []string{"group/work-ssh"} },
	} {
		p, err := PlanImport("12345678123456781234567812345678", base)
		if err != nil {
			t.Fatal(err)
		}
		change(&p)
		b, _ := json.Marshal(p.Setup)
		p.Receipt.Digest = configRevision(b)
		if _, err = ApplyImport(p); err == nil {
			t.Fatal("accepted malformed import")
		}
	}
}
