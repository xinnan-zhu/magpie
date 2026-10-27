package sshremote

import (
	"context"
	"errors"

	"github.com/yetone/magpie/internal/provider"
)

type copyPlan struct{ Plan provider.ImportPlan }

// CopyPreview is safe to send to a page: names and new ids, never credentials.
type CopyEntry struct {
	Name    string   `json:"name"`
	ID      string   `json:"id"`
	Details []string `json:"details"`
}
type CopyPreview struct {
	Entries []CopyEntry       `json:"entries"`
	ID      string            `json:"id"`
	IDs     map[string]string `json:"ids"`
	Keys    bool              `json:"keys"`
}

func (c *Connection) PreviewCopy(ctx context.Context, providers, groups []string, keys bool) (CopyPreview, error) {
	setup, err := provider.SelectCopy(providers, groups, keys)
	if err != nil {
		return CopyPreview{}, err
	}
	var p provider.ImportPlan
	if err = c.API(ctx, "/api/ssh/transfer/plan", map[string]any{"id": newID(), "setup": setup}, &p); err != nil {
		return CopyPreview{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return CopyPreview{}, errors.New("SSH connection is closed")
	}
	c.plan = &copyPlan{Plan: p}
	entries := []CopyEntry{}
	for _, q := range p.Setup.Providers {
		details := []string{}
		for _, u := range []string{q.Chat, q.Responses, q.Anthropic, q.Gemini, q.Decide} {
			if u != "" {
				details = append(details, u)
			}
		}
		details = append(details, q.Models...)
		entries = append(entries, CopyEntry{Name: q.Name, ID: q.ID, Details: details})
	}
	for _, g := range p.Setup.Groups {
		entries = append(entries, CopyEntry{Name: g.Name, ID: "group/" + g.ID, Details: g.Members})
	}
	return CopyPreview{ID: p.ID, IDs: p.Receipt.IDs, Keys: keys, Entries: entries}, nil
}
func (c *Connection) ApplyCopy(ctx context.Context, id string) (provider.ImportReceipt, error) {
	c.mu.Lock()
	p := c.plan
	c.mu.Unlock()
	if p == nil || p.Plan.ID != id {
		return provider.ImportReceipt{}, errors.New("preview this selection again")
	}
	var receipt provider.ImportReceipt
	err := c.API(ctx, "/api/ssh/transfer/apply", p.Plan, &receipt)
	return receipt, err
}
