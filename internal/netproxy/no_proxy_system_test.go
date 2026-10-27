package netproxy

import (
	"net/url"
	"testing"
	"time"
)

func TestNoProxyDoesNotFallThroughToSystem(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "http://127.0.0.1:11")
	t.Setenv("NO_PROXY", "trace.example.invalid")
	sysCache.Lock()
	old, at := sysCache.p, sysCache.at
	sysCache.p, sysCache.at = Proxy{URL: "http://127.0.0.1:12"}, time.Now()
	sysCache.Unlock()
	t.Cleanup(func() { sysCache.Lock(); sysCache.p, sysCache.at = old, at; sysCache.Unlock() })
	u, _ := url.Parse("http://trace.example.invalid/path")
	if p, err := For(u); err != nil || p != nil {
		t.Fatalf("NO_PROXY fell through to system: %v %v", p, err)
	}
}
