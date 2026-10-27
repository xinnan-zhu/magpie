package gui

import (
	"encoding/json"
	"net/http"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/sshremote"
	"github.com/yetone/magpie/internal/update"
)

func sshDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, sshremote.MaxMessage))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, e)
		return false
	}
	return true
}
func sshTransferRoutes(mux sshRouteMux) {
	mux.HandleFunc("POST /api/ssh/transfer/plan", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID    string             `json:"id"`
			Setup provider.SetupCopy `json:"setup"`
		}
		if !sshDecode(w, r, &in) {
			return
		}
		p, e := provider.PlanImport(in.ID, in.Setup)
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, p)
	})
	mux.HandleFunc("POST /api/ssh/transfer/apply", func(w http.ResponseWriter, r *http.Request) {
		var in provider.ImportPlan
		if !sshDecode(w, r, &in) {
			return
		}
		out, e := provider.ApplyImport(in)
		if e != nil {
			fail(w, e)
			return
		}
		agent.SyncCatalogSoon()
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /api/ssh/transfer", func(w http.ResponseWriter, r *http.Request) {
		receipt, found, e := provider.ImportStatus(r.URL.Query().Get("id"))
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, map[string]any{"found": found, "receipt": receipt})
	})
	mux.HandleFunc("POST /api/ssh/upstream", func(w http.ResponseWriter, r *http.Request) {
		var in sshremote.Upstream
		if !sshDecode(w, r, &in) {
			return
		}
		out, e := sshremote.CheckUpstream(r.Context(), in)
		if e != nil {
			fail(w, e)
			return
		}
		agent.SyncCatalogSoon()
		writeJSON(w, out)
	})
}
func sshDesktopOperations(mux *http.ServeMux, desktop func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/ssh/connections/{id}/preview/{agent}", desktop(func(w http.ResponseWriter, r *http.Request) {
		c, e := sshremote.Desktop.Get(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		var out json.RawMessage
		if e = c.API(r.Context(), "/api/ssh/preview/"+r.PathValue("agent"), nil, &out); e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/deploy", desktop(func(w http.ResponseWriter, r *http.Request) {
		c, e := sshremote.Desktop.Get(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		name, e := sshremote.ArtifactName(c.Inspection)
		if e != nil {
			fail(w, e)
			return
		}
		ctx, cancel := c.OperationContext(r.Context())
		defer cancel()
		path, digest, cleanup, e := update.DownloadCLI(ctx, "linux", name[len("magpie-cli-linux-"):])
		if e != nil {
			fail(w, e)
			return
		}
		defer cleanup()
		if e = sshremote.Deploy(ctx, c.Host, c.Inspection, path, digest); e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, true)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/tunnel", desktop(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Port int `json:"port"`
		}
		if !sshDecode(w, r, &in) {
			return
		}
		c, e := sshremote.Desktop.Get(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		gw := served.Load()
		if gw == nil {
			http.Error(w, "this desktop must serve the local gateway; use Gateway to take it over first", 409)
			return
		}
		out, e := c.StartTunnel(r.Context(), in.Port, gw.SSHFront)
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("GET /api/ssh/copy-choices", desktop(func(w http.ResponseWriter, r *http.Request) {
		ps, e := provider.Stored()
		if e != nil {
			fail(w, e)
			return
		}
		gs, e := provider.StoredGroups()
		if e != nil {
			fail(w, e)
			return
		}
		type choice struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		choices := []choice{}
		groups := []choice{}
		for _, p := range ps {
			if _, err := provider.SelectCopy([]string{p.ID}, nil, false); err == nil {
				choices = append(choices, choice{p.ID, p.Name})
			}
		}
		for _, g := range gs {
			if !g.Auto && !g.Hidden {
				groups = append(groups, choice{g.ID, g.Name})
			}
		}
		writeJSON(w, map[string]any{"providers": choices, "groups": groups})
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/copy/preview", desktop(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Providers []string `json:"providers"`
			Groups    []string `json:"groups"`
			Keys      bool     `json:"keys"`
		}
		if !sshDecode(w, r, &in) {
			return
		}
		c, e := sshremote.Desktop.Get(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		out, e := c.PreviewCopy(r.Context(), in.Providers, in.Groups, in.Keys)
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/copy/apply", desktop(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		if !sshDecode(w, r, &in) {
			return
		}
		c, e := sshremote.Desktop.Get(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		out, e := c.ApplyCopy(r.Context(), in.ID)
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, out)
	}))
}
