package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/provider"
)

func TestSSHFrontRequiresItsKeyOnLoopbackWithoutLAN(t *testing.T) {
	fresh(t)
	key, err := access.NewMachineKey("SSH one")
	if err != nil {
		t.Fatal(err)
	}
	other, err := access.NewMachineKey("SSH two")
	if err != nil {
		t.Fatal(err)
	}
	handler := New().SSHFront(key.ID)
	ask := func(token, method, path string) int {
		req := httptest.NewRequest(method, "http://127.0.0.1:4321"+path, nil)
		req.RemoteAddr = "127.0.0.1:4322"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	for _, token := range []string{"", Token, TokenFor("claude"), other.Secret, "wrong"} {
		if got := ask(token, "GET", "/v1/models"); got != 401 {
			t.Fatalf("%q got %d, want 401", token, got)
		}
	}
	if remoteKeyed() {
		t.Fatal("fixture unexpectedly enabled LAN")
	}
	if got := ask(key.Secret, "GET", "/v1/models"); got != 200 {
		t.Fatalf("authenticated SSH request with LAN off got %d", got)
	}
	for _, p := range []string{"/api/state", "/_magpie/claude/tool", "/mcp/magpie/tools", GoroutinesPath, "/v1/../api/state"} {
		if got := ask(key.Secret, "GET", p); got != 404 {
			t.Fatalf("admin route %s got %d", p, got)
		}
	}
	if _, err = access.Update("off-key", access.Change{Key: key.ID}); err != nil {
		t.Fatal(err)
	}
	if got := ask(key.Secret, "GET", "/v1/models"); got != 401 {
		t.Fatalf("revoked tunnel got %d", got)
	}
	// A transport marker cannot be forged on the usual listener.
	req := httptest.NewRequest("GET", "http://127.0.0.1/v1/models", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Magpie-SSH", "1")
	req.Header.Set("Authorization", "Bearer "+other.Secret)
	w := httptest.NewRecorder()
	New().Front().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestSSHFrontUsesNormalModelAndBudgetGuards(t *testing.T) {
	fresh(t)
	budget.Forget()
	pinLimitClock(t)
	var vendorCalls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vendorCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":300,"completion_tokens":50}}`))
	}))
	defer vendor.Close()
	if err := provider.Save(provider.Provider{ID: "fixture", Name: "Fixture", Chat: vendor.URL + "/v1", Models: []string{"yes", "no"}, Key: "fixture"}); err != nil {
		t.Fatal(err)
	}
	key, err := access.NewMachineKey("SSH")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = access.Update("models-key", access.Change{Key: key.ID, Models: []string{"fixture/yes"}}); err != nil {
		t.Fatal(err)
	}
	h := New().SSHFront(key.ID)
	ask := func(model string) int {
		req := httptest.NewRequest("POST", "http://127.0.0.1/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"fixture"}]}`))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if got := ask("fixture/no"); got != 403 {
		t.Fatal("model restriction", got)
	}
	if vendorCalls.Load() != 0 {
		t.Fatal("restricted request reached vendor")
	}
	if got := ask("fixture/yes"); got != 200 {
		t.Fatal("allowed request", got)
	}
	if vendorCalls.Load() != 1 {
		t.Fatal(vendorCalls.Load())
	}
	setLimit(t, key.ID, &access.Limit{Period: "day", Tokens: 1})
	if got := ask("fixture/yes"); got != 429 {
		t.Fatal("spent SSH key let in", got)
	}
	if vendorCalls.Load() != 1 {
		t.Fatal("spent key reached vendor")
	}
}
func TestSSHBridgeScopesExactAndRemappedTools(t *testing.T) {
	b := &subscriptionBridge{calls: map[string]*subscriptionRun{}, runs: map[string]*subscriptionRun{}}
	args := json.RawMessage(`{"path":"file"}`)
	run := &subscriptionRun{sshScope: "host-a", model: "m", token: "fixture", shown: []shownCall{{id: "own-id", name: "read", args: string(args)}}, parkedAt: time.Now(), told: "history"}
	b.runs[run.token] = run
	b.calls["own-id"] = run
	req := &Request{sshScope: "host-b", Model: "m", Messages: []Message{{Role: "assistant", Parts: []Part{{Kind: ToolCall, ID: "own-id", Name: "read", Args: args}}}, {Role: "user", Parts: []Part{{Kind: ToolResult, CallID: "own-id", Text: "other host"}}}}}
	if found, _, _ := b.match(req); found != nil {
		t.Fatal("another host answered the tool call")
	}
	req.sshScope = "host-a"
	if found, _, _ := b.match(req); found != run {
		t.Fatal("own host did not match")
	}
	calls := []Part{{Kind: ToolCall, ID: "client-id", Name: "read", Args: args}}
	results := []Part{{Kind: ToolResult, CallID: "client-id"}}
	if found, _, _ := b.remap("m", calls, results, map[string]bool{"history": true}, "host-b"); found != nil {
		t.Fatal("remapped another host's tool call")
	}
	if found, _, _ := b.remap("m", calls, results, map[string]bool{"history": true}, "host-a"); found != run {
		t.Fatal("own remapped call did not match")
	}
	dir := t.TempDir()
	req = &Request{System: "<cwd>" + dir + "</cwd>"}
	if got := clientWorkDir(req); got != dir {
		t.Fatal("local behavior changed", got)
	}
	req.sshScope = "host-a"
	if got := clientWorkDir(req); got != "" {
		t.Fatal("remote cwd used as local cwd", got)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/messages", nil)
	r = r.WithContext(context.WithValue(r.Context(), sshTransport{}, "host-a"))
	if local(r) {
		t.Fatal("trusted SSH context became local")
	}
}
