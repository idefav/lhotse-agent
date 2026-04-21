package domainpolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerReloadAppendsAppAndIPAndWritesCache(t *testing.T) {
	var gotApp, gotIP string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotApp = r.URL.Query().Get("app")
		gotIP = r.URL.Query().Get("ip")
		_, _ = w.Write([]byte(`{"rules":[{"direction":"outbound","mode":"default_deny","allowList":["api.example.com"],"blockList":[]}]}`))
	}))
	defer server.Close()

	cacheFile := filepath.Join(t.TempDir(), "policy.json")
	manager := NewManager(Options{
		URL:          server.URL + "?existing=1",
		CacheFile:    cacheFile,
		FetchTimeout: time.Second,
		Scope:        ScopeOutbound,
		AppName:      "hermes-agent",
		InstanceIP:   "10.1.2.3",
	})

	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if gotApp != "hermes-agent" || gotIP != "10.1.2.3" {
		t.Fatalf("query app=%q ip=%q, want app hermes-agent ip 10.1.2.3", gotApp, gotIP)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("cache file stat error = %v", err)
	}
	if decision := manager.Evaluate(DirectionOutbound, "api.example.com", "203.0.113.10:443"); !decision.Allowed {
		t.Fatalf("Allowed = false, want true")
	}
	if decision := manager.Evaluate(DirectionOutbound, "other.example.com", "203.0.113.10:443"); decision.Allowed {
		t.Fatalf("Allowed = true, want false")
	}
}

func TestManagerLoadsCacheWhenRemoteUnavailable(t *testing.T) {
	cacheFile := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(cacheFile, []byte(`{"rules":[{"direction":"outbound","mode":"default_allow","allowList":[],"blockList":["198.51.100.0/24"]}]}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := NewManager(Options{
		URL:        "http://127.0.0.1:1/policy",
		CacheFile:  cacheFile,
		Scope:      ScopeOutbound,
		AppName:    "app",
		InstanceIP: "10.1.2.3",
	})

	manager.Start()
	defer manager.Stop()

	decision := manager.Evaluate(DirectionOutbound, "", "198.51.100.10:443")
	if decision.Allowed {
		t.Fatalf("Allowed = true, want false from cached policy")
	}
}

func TestManagerRemoteFailureWithoutCacheDefaultsAllow(t *testing.T) {
	manager := NewManager(Options{
		URL:        "http://127.0.0.1:1/policy",
		CacheFile:  filepath.Join(t.TempDir(), "missing.json"),
		Scope:      ScopeOutbound,
		AppName:    "app",
		InstanceIP: "10.1.2.3",
	})

	manager.Start()
	defer manager.Stop()

	decision := manager.Evaluate(DirectionOutbound, "blocked.example.com", "198.51.100.10:443")
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true with no policy")
	}
}
