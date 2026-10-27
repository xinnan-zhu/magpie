package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/sshremote"
)

// sshAgents uses the serving process's original adapters and model spelling.
// It is an explicit management read, separate from the side-effect-free context
// probe. No local HOME is ever replaced with a remote user's environment.
func sshAgents() []agentJSON {
	out := []agentJSON{}
	for _, id := range []string{"claude", "codex"} {
		a, err := agent.Find(id)
		if err != nil || !a.Detected() {
			continue
		}
		vals := a.Values()
		out = append(out, agentJSON{ID: a.ID, Name: a.Name, Icon: a.Icon, Path: a.Path, Fields: agentFields(a, vals), Wired: a.Wired(), Drift: a.Drift()})
	}
	return out
}
func sshAgent(id string) (*agent.Agent, error) {
	if id != "claude" && id != "codex" {
		return nil, errors.New("SSH management currently supports Claude Code and Codex")
	}
	a, err := agent.Find(id)
	if err != nil {
		return nil, err
	}
	if !a.Detected() {
		return nil, errors.New("this agent was not found in the remote service's environment")
	}
	return a, nil
}

func sshDisconnectPreview(id string) ([]agent.FileChange, string, error) {
	a, e := sshAgent(id)
	if e != nil {
		return nil, "", e
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, "", e
	}
	changes, e := agent.DisconnectPreview(a, exe)
	if e != nil {
		return nil, "", e
	}
	b, e := json.Marshal(changes)
	if e != nil {
		return nil, "", e
	}
	sum := sha256.Sum256(b)
	return changes, hex.EncodeToString(sum[:]), nil
}
func sshAgentRoutes(mux sshRouteMux) {
	mux.HandleFunc("GET /api/ssh/preview/{id}", func(w http.ResponseWriter, r *http.Request) {
		changes, revision, e := sshDisconnectPreview(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, map[string]any{"changes": changes, "revision": revision})
	})
	mux.HandleFunc("GET /api/ssh/agents", func(rw http.ResponseWriter, r *http.Request) { writeJSON(rw, sshAgents()) })
	mux.HandleFunc("POST /api/ssh/pick", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent string `json:"agent"`
			Field string `json:"field"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, sshremote.MaxMessage)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		a, err := sshAgent(in.Agent)
		if err != nil {
			fail(rw, err)
			return
		}
		if a.Field(in.Field) == nil {
			fail(rw, errors.New("unknown agent field"))
			return
		}
		if a.Drift() != nil {
			fail(rw, errors.New("the agent configuration changed; review it in the remote Web page before applying another model"))
			return
		}
		if err = a.Pick(in.Field, in.Value); err != nil {
			fail(rw, err)
			return
		}
		notice := ""
		if a.Notice != nil {
			notice = a.Notice()
		}
		writeJSON(rw, map[string]any{"agents": sshAgents(), "notice": notice})
	})
	mux.HandleFunc("POST /api/ssh/agent", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent    string `json:"agent"`
			Action   string `json:"action"`
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4096)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		a, err := sshAgent(in.Agent)
		if err != nil {
			fail(rw, err)
			return
		}
		switch in.Action {
		case "connect":
			if a.Drift() != nil {
				err = errors.New("review configuration drift in the remote Web page first")
			} else {
				err = a.Connect()
			}
		case "disconnect":
			_, revision, e := sshDisconnectPreview(a.ID)
			if e != nil {
				err = e
			} else if in.Revision == "" || in.Revision != revision {
				err = errors.New("agent configuration changed; preview disconnect again")
			} else {
				err = a.Disconnect()
			}
		default:
			err = errors.New("unsupported agent action")
		}
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"agents": sshAgents()})
	})
}
