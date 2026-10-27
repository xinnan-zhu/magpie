package gui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/sshremote"
)

func sshRoutes(mux *http.ServeMux, w Windows) {
	sshAgentRoutes(sshBoundMux{mux})
	sshTransferRoutes(sshBoundMux{mux})
	// This is authenticated by the remote Web's existing guard. It deliberately
	// doesn't call state(), provider.All(), Drift, or a CLI probe.
	mux.HandleFunc("GET /api/ssh/context", func(rw http.ResponseWriter, r *http.Request) {
		c, err := sshremote.ReadContext(Version)
		if err != nil {
			fail(rw, err)
			return
		}
		c.Gateway = gateway.URL()
		writeJSON(rw, c)
	})
	desktop := func(h http.HandlerFunc) http.HandlerFunc {
		return func(rw http.ResponseWriter, r *http.Request) {
			if w == nil || isWeb(w) {
				http.Error(rw, "SSH management is available in the desktop app", http.StatusForbidden)
				return
			}
			_, err := pluginrt.Refresh()
			rt := pluginrt.Running()
			if err != nil || rt == nil || !rt.Active("ssh") {
				http.Error(rw, "SSH plugin is off", http.StatusForbidden)
				return
			}
			rw.Header().Set("Cache-Control", "no-store")
			h(rw, r)
		}
	}
	decode := func(rw http.ResponseWriter, r *http.Request, v any) bool {
		dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, sshremote.MaxMessage))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			fail(rw, err)
			return false
		}
		return true
	}
	sshDesktopOperations(mux, desktop)
	mux.HandleFunc("GET /api/ssh/hosts", desktop(func(rw http.ResponseWriter, r *http.Request) {
		hs, err := sshremote.Hosts()
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, hs)
	}))
	mux.HandleFunc("POST /api/ssh/hosts", desktop(func(rw http.ResponseWriter, r *http.Request) {
		var h sshremote.Host
		if !decode(rw, r, &h) {
			return
		}
		saved, err := sshremote.SaveHost(h)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, saved)
	}))
	mux.HandleFunc("DELETE /api/ssh/hosts/{id}", desktop(func(rw http.ResponseWriter, r *http.Request) {
		if err := sshremote.DeleteHost(r.PathValue("id")); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, true)
	}))
	mux.HandleFunc("POST /api/ssh/connections", desktop(func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Host string `json:"host"`
		}
		if !decode(rw, r, &in) {
			return
		}
		hs, err := sshremote.Hosts()
		if err != nil {
			fail(rw, err)
			return
		}
		for _, h := range hs {
			if h.ID == in.Host {
				ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				defer cancel()
				c, err := sshremote.Desktop.Connect(ctx, h)
				if err != nil {
					fail(rw, err)
					return
				}
				writeJSON(rw, c)
				return
			}
		}
		fail(rw, errors.New("unknown SSH host"))
	}))
	mux.HandleFunc("DELETE /api/ssh/connections/{id}", desktop(func(rw http.ResponseWriter, r *http.Request) {
		sshremote.Desktop.Disconnect(r.PathValue("id"))
		writeJSON(rw, true)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/web", desktop(func(rw http.ResponseWriter, r *http.Request) {
		c, err := sshremote.Desktop.Get(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		link, err := c.OpenWeb(r.Context())
		if err != nil {
			fail(rw, err)
			return
		}
		if err = w.OpenURL(link); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, true)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/service", desktop(func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Action   string `json:"action"`
			Revision string `json:"revision,omitempty"`
		}
		if !decode(rw, r, &in) {
			return
		}
		if in.Action != "install" && in.Action != "start" {
			fail(rw, errors.New("unsupported service action"))
			return
		}
		c, err := sshremote.Desktop.Get(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		var out sshremote.Inspection
		target := c.Inspection.Context.Target
		if c.Inspection.Shell != nil {
			target = c.Inspection.Shell.Target
		}
		if err = c.Call(r.Context(), sshremote.Request{Method: "service-" + in.Action, Target: target}, &out); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, out)
	}))
	mux.HandleFunc("GET /api/ssh/connections/{id}/agents", desktop(func(rw http.ResponseWriter, r *http.Request) {
		c, err := sshremote.Desktop.Get(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		var out json.RawMessage
		if err = c.API(r.Context(), "/api/ssh/agents", nil, &out); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, out)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/pick", desktop(func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent string `json:"agent"`
			Field string `json:"field"`
			Value string `json:"value"`
		}
		if !decode(rw, r, &in) {
			return
		}
		c, err := sshremote.Desktop.Get(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		var out json.RawMessage
		if err = c.API(r.Context(), "/api/ssh/pick", in, &out); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, out)
	}))
	mux.HandleFunc("POST /api/ssh/connections/{id}/agent", desktop(func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent    string `json:"agent"`
			Action   string `json:"action"`
			Revision string `json:"revision,omitempty"`
		}
		if !decode(rw, r, &in) {
			return
		}
		c, err := sshremote.Desktop.Get(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		var out json.RawMessage
		if err = c.API(r.Context(), "/api/ssh/agent", in, &out); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, out)
	}))

}

// The service checks context again on the mutation's own request. A preflight
// probe alone cannot bind a later write if a service was restarted meanwhile.
type sshRouteMux interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}
type sshBoundMux struct{ *http.ServeMux }

func (m sshBoundMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.ServeMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		c, e := sshremote.ReadContext(Version)
		if e != nil {
			fail(w, e)
			return
		}
		if r.Header.Get("X-Magpie-SSH-Target") != c.Target {
			http.Error(w, "remote environment changed; reconnect", http.StatusConflict)
			return
		}
		h(w, r)
	})
}
