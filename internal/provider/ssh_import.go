package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// SetupCopy is the portable subset of a backup bundle. It deliberately carries
// neither sign-ins, settings, caller keys, plugin code, nor agent files.
type SetupCopy struct {
	Providers []Provider `json:"providers"`
	Groups    []Group    `json:"groups"`
}
type ImportReceipt struct {
	Digest  string            `json:"digest"`
	Created time.Time         `json:"created"`
	IDs     map[string]string `json:"ids"`
}
type ImportPlan struct {
	ID       string        `json:"id"`
	Revision string        `json:"revision"`
	Setup    SetupCopy     `json:"setup"`
	Receipt  ImportReceipt `json:"receipt"`
}

// portableProvider is an allow list, so a new machine-local field is never
// accidentally carried. Secondary API keys are copied only by explicit choice.
func portableProvider(p Provider, keys bool) (Provider, error) {
	if p.ID == "" || p.ID != Slug(p.ID) || p.ID == "magpie" || slices.Contains(accountIDs, p.ID) || p.Account != nil || p.IsRemoteMagpie() || p.IsVertex() {
		return Provider{}, errors.New("only API providers can be copied")
	}
	if pr := Preset(p.Preset); pr != nil && pr.Kind != KindVendor && pr.Kind != KindRelay {
		return Provider{}, errors.New("only API providers can be copied")
	}
	q := Provider{ID: p.ID, Name: p.Name, Preset: p.Preset, Catalog: p.Catalog, Chat: p.Chat, Responses: p.Responses, Anthropic: p.Anthropic, Gemini: p.Gemini, Decide: p.Decide, Models: slices.Clone(p.Models), NoModels: p.NoModels, PickedFrom: slices.Clone(p.PickedFrom), Contexts: p.Contexts, Family: p.Family, Off: p.Off, Unlisted: p.Unlisted, Routing: p.Routing, Affinity: p.Affinity, MaxConcurrency: p.MaxConcurrency, MaxRPM: p.MaxRPM, QueueLimit: p.QueueLimit, QueueWait: p.QueueWait, PriceRate: p.PriceRate, Searches: p.Searches, CodexAsSent: p.CodexAsSent, AdditionalTools: p.AdditionalTools, NativeCompaction: p.NativeCompaction, PassUserAgent: p.PassUserAgent, AuthHeader: p.AuthHeader}
	for _, base := range []string{q.Chat, q.Responses, q.Anthropic, q.Gemini, q.Decide} {
		if base == "" {
			continue
		}
		u, err := url.Parse(base)
		if err != nil {
			return Provider{}, err
		}
		ip := net.ParseIP(u.Hostname())
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) {
			return Provider{}, fmt.Errorf("%s has a machine-local or credential-bearing endpoint; configure it on the target", p.ID)
		}
	}
	q.Headers = map[string]string{}
	for k, v := range p.Headers {
		if keys || !SecretHeader(k) {
			q.Headers[k] = v
		}
	}
	if keys {
		q.Key, q.KeyName, q.Keys, q.KeyProtocol, q.KeyWeight, q.KeyPlan = p.Key, p.KeyName, slices.Clone(p.Keys), p.KeyProtocol, p.KeyWeight, p.KeyPlan
	}
	return q, nil
}

// SelectCopy reads stored data only. It does not discover sign-ins, initialize
// providers, or fetch catalogs. Dependencies must be selected explicitly.
func SelectCopy(ids, groups []string, keys bool) (SetupCopy, error) {
	f, err := read()
	if err != nil {
		return SetupCopy{}, err
	}
	out := SetupCopy{Providers: []Provider{}, Groups: []Group{}}
	for _, id := range ids {
		i := slices.IndexFunc(f.Providers, func(p Provider) bool { return p.ID == id })
		if i < 0 {
			return out, fmt.Errorf("provider %s was not found", id)
		}
		p, err := portableProvider(f.Providers[i], keys)
		if err != nil {
			return out, err
		}
		out.Providers = append(out.Providers, p)
	}
	for _, id := range groups {
		i := slices.IndexFunc(f.Groups, func(g Group) bool { return g.ID == id })
		if i < 0 {
			return out, fmt.Errorf("group %s was not found", id)
		}
		out.Groups = append(out.Groups, f.Groups[i])
	}
	// Deep copy maps before the plan rewrites references.
	b, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func importID(id string) bool { return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == "" }
func portableGroup(g Group) bool {
	return g.ID != "" && g.ID == GroupSlug(g.ID) && !g.Auto && !g.Hidden && len(g.Match) == 0 && len(g.Matched) == 0 && len(g.Rules) == 0 && g.Classifier == ""
}

// Every copied reference stays within the explicit selection. Do not silently
// attach a copied group to a coincidentally named provider on the target.
func validateCopy(s SetupCopy) error {
	ids := map[string]bool{}
	for _, p := range s.Providers {
		if ids[p.ID] {
			return errors.New("duplicate provider in selection")
		}
		ids[p.ID] = true
	}
	for _, g := range s.Groups {
		if ids[GroupPrefix+g.ID] {
			return errors.New("duplicate group in selection")
		}
		ids[GroupPrefix+g.ID] = true
	}
	for _, g := range s.Groups {
		if !portableGroup(g) {
			return errors.New("copy supports explicit routing groups; configure patterns and classifiers on the target")
		}
		if err := groupsInGroup(g, s.Groups); err != nil {
			return err
		}
		refs := slices.Clone(g.Members)
		refs = append(refs, g.Off...)
		refs = append(refs, g.Fast...)
		if g.Pick != "" {
			refs = append(refs, g.Pick)
		}
		for ref := range g.Weights {
			refs = append(refs, ref)
		}
		for _, ref := range refs {
			head, tail, ok := strings.Cut(ref, "/")
			if !ok || tail == "" || (head == "group" && !ids[ref]) || (head != "group" && !ids[head]) {
				return fmt.Errorf("select dependency %s too", ref)
			}
		}
	}
	return nil
}

func planImport(f file, id string, setup SetupCopy) (ImportPlan, error) {
	// Remapping must not rewrite the caller's saved selection or references.
	b, err := json.Marshal(setup)
	if err != nil {
		return ImportPlan{}, err
	}
	setup = SetupCopy{}
	if err = json.Unmarshal(b, &setup); err != nil {
		return ImportPlan{}, err
	}
	plan := ImportPlan{ID: id, Revision: f.revision, Setup: setup, Receipt: ImportReceipt{Created: time.Now().UTC(), IDs: map[string]string{}}}
	if !importID(id) {
		return plan, errors.New("invalid import operation")
	}
	if len(setup.Providers)+len(setup.Groups) == 0 || len(setup.Providers)+len(setup.Groups) > 100 {
		return plan, errors.New("choose between 1 and 100 entries")
	}
	if err := validateCopy(setup); err != nil {
		return plan, err
	}
	used := map[string]bool{}
	for _, p := range f.Providers {
		used[p.ID] = true
	}
	for _, g := range f.Groups {
		used["group/"+g.ID] = true
	}
	add := func(old string) error {
		if _, dup := plan.Receipt.IDs[old]; dup {
			return errors.New("duplicate selection")
		}
		suffix := "-ssh"
		next := old + suffix
		for n := 2; used[next]; n++ {
			next = fmt.Sprintf("%s%s-%d", old, suffix, n)
		}
		used[next] = true
		plan.Receipt.IDs[old] = next
		return nil
	}
	for i, p := range setup.Providers {
		q, err := portableProvider(p, true)
		if err != nil {
			return plan, err
		}
		if err = add(p.ID); err != nil {
			return plan, err
		}
		q.ID = plan.Receipt.IDs[p.ID]
		plan.Setup.Providers[i] = q
	}
	for _, g := range setup.Groups {
		if !portableGroup(g) {
			return plan, errors.New("copy supports explicit routing groups; configure patterns and classifiers on the target")
		}
		if err := add("group/" + g.ID); err != nil {
			return plan, err
		}
	}
	ref := func(old string) (string, error) {
		if old == "" {
			return "", nil
		}
		head, tail, ok := strings.Cut(old, "/")
		if head == "group" {
			next, found := plan.Receipt.IDs[old]
			if !found {
				return "", fmt.Errorf("select dependency %s too", old)
			}
			return next, nil
		}
		next, found := plan.Receipt.IDs[head]
		if !ok || !found {
			return "", fmt.Errorf("select dependency %s too", old)
		}
		return next + "/" + tail, nil
	}
	for i, g := range setup.Groups {
		g.ID = strings.TrimPrefix(plan.Receipt.IDs["group/"+g.ID], "group/")
		for _, list := range [][]string{g.Members, g.Off, g.Fast} {
			for n, v := range list {
				r, e := ref(v)
				if e != nil {
					return plan, e
				}
				list[n] = r
			}
		}
		var err error
		g.Pick, err = ref(g.Pick)
		if err != nil {
			return plan, err
		}
		weights := map[string]int{}
		for k, v := range g.Weights {
			r, e := ref(k)
			if e != nil {
				return plan, e
			}
			weights[r] = v
		}
		g.Weights = weights
		plan.Setup.Groups[i] = g
	}
	b, _ = json.Marshal(plan.Setup)
	plan.Receipt.Digest = configRevision(b)
	return plan, nil
}
func PlanImport(id string, setup SetupCopy) (ImportPlan, error) {
	f, e := read()
	if e != nil {
		return ImportPlan{}, e
	}
	return planImport(f, id, setup)
}

// ApplyImport atomically adds every selected entry and its receipt. A lost SSH
// reply can be queried or explicitly retried with the same operation id. Every
// provider writer's store checks its generation under the same file lock.
func ApplyImport(p ImportPlan) (ImportReceipt, error) {
	if !importID(p.ID) || len(p.Setup.Providers)+len(p.Setup.Groups) == 0 || len(p.Setup.Providers)+len(p.Setup.Groups) > 100 {
		return ImportReceipt{}, errors.New("invalid import plan")
	}

	f, e := read()
	if e != nil {
		return ImportReceipt{}, e
	}
	b, _ := json.Marshal(p.Setup)
	digest := configRevision(b)
	if digest != p.Receipt.Digest {
		return ImportReceipt{}, errors.New("import contents changed; preview again")
	}
	if receipt, ok := f.SSHImports[p.ID]; ok {
		if receipt.Digest != digest {
			return ImportReceipt{}, errors.New("operation id was already used")
		}
		return receipt, nil
	}
	if f.revision != p.Revision {
		return ImportReceipt{}, errors.New("remote configuration changed; preview again")
	}
	if err := validateCopy(p.Setup); err != nil {
		return ImportReceipt{}, err
	}
	// Revalidate the already remapped payload without assigning ids again.
	for _, q := range p.Setup.Providers {
		clean, err := portableProvider(q, true)
		if err != nil {
			return ImportReceipt{}, err
		}
		if !sameStored(clean, q) {
			return ImportReceipt{}, errors.New("nonportable provider settings in plan")
		}
		for _, old := range f.Providers {
			if old.ID == q.ID {
				return ImportReceipt{}, errors.New("provider id is occupied")
			}
		}
	}
	for _, g := range p.Setup.Groups {
		if !portableGroup(g) {
			return ImportReceipt{}, errors.New("nonportable group in plan")
		}
		for _, old := range f.Groups {
			if old.ID == g.ID {
				return ImportReceipt{}, errors.New("group id is occupied")
			}
		}
	}
	f.Providers = append(f.Providers, p.Setup.Providers...)
	f.Groups = append(f.Groups, p.Setup.Groups...)
	if f.SSHImports == nil {
		f.SSHImports = map[string]ImportReceipt{}
	}
	if len(f.SSHImports) >= 32 {
		oldest := ""
		for id, r := range f.SSHImports {
			if oldest == "" || r.Created.Before(f.SSHImports[oldest].Created) {
				oldest = id
			}
		}
		delete(f.SSHImports, oldest)
	}
	f.SSHImports[p.ID] = p.Receipt
	return p.Receipt, store(f)
}
func ImportStatus(id string) (ImportReceipt, bool, error) {
	f, e := read()
	r, ok := f.SSHImports[id]
	return r, ok, e
}

// AddSSHUpstream adds one managed Remote Magpie provider without replacing an
// existing entry. Its id is the tunnel operation, making a lost reply safe to
// query. Removing the tunnel never removes this configuration or its picks.
func AddSSHUpstream(id, name, base, key, source string, models []string) (string, error) {
	if !strings.HasPrefix(id, "ssh-") || len(id) != 36 || Slug(id) != id || !strings.HasPrefix(base, "http://127.0.0.1:") || key == "" || source == "" || len(source) > 256 {
		return "", errors.New("invalid SSH upstream")
	}
	f, e := read()
	if e != nil {
		return "", e
	}
	for i, p := range f.Providers {
		if p.ID != id {
			continue
		}
		if !p.IsRemoteMagpie() || p.SSHSource != source || p.Anthropic != base || p.Chat != base+"/v1" || p.Responses != base+"/v1" || len(p.Headers) > 0 {
			return "", errors.New("SSH upstream was edited or belongs to another source; review it on the remote Web page")
		}
		if p.Key == key {
			return id, nil
		}
		p.Key = key
		f.Providers[i] = p
		return id, store(f)
	}
	p := Provider{SSHSource: source, ID: id, Name: name, Preset: RemoteMagpiePreset, Key: key, Chat: base + "/v1", Responses: base + "/v1", Anthropic: base, Models: models}
	f.Providers = append(f.Providers, p)
	return id, store(f)
}
