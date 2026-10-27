package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/access"
)

type sshTransport struct{}

func sshScope(r *http.Request) string {
	s, _ := r.Context().Value(sshTransport{}).(string)
	return s
}

// SSHFront is a dedicated loopback ingress for one SSH connection. It shares
// this Server's routing and subscription bridge, but never loopback trust. No
// header can opt into this context. Keys remain revocable through Gateway keys.
func (s *Server) SSHFront(keyID string) http.Handler {
	front := s.Front()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := access.Authenticate(callerKey(r))
		if !ok || keyID == "" || who.KeyID != keyID {
			http.Error(w, "send this SSH connection's enabled gateway key", http.StatusUnauthorized)
			return
		}
		// Only inference and model discovery. No administration, callbacks,
		// goroutine dump, or the local library's MCP tools cross this ingress.
		p := r.URL.Path
		allowed := r.Method == "GET" && (p == "/v1/models" || p == "/models") ||
			r.Method == "POST" && (p == "/v1/messages" || p == "/v1/messages/count_tokens" ||
				p == "/v1/chat/completions" || p == "/v1/responses" || p == "/v1/responses/compact")
		if !allowed || r.URL.RawPath != "" || strings.Contains(p, "..") {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(context.WithValue(r.Context(), sshTransport{}, keyID))
		// Scope session affinity too, including identical caller-supplied ids.
		for _, h := range append([]string{SessionHeader}, sessionHeaders...) {
			if v := r.Header.Get(h); v != "" {
				r.Header.Set(h, keyID+":"+v)
			}
		}
		front.ServeHTTP(w, r)
	})
}
