package credential

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetcherReturnsNilOnNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	fetcher := NewFetcher(server.URL)
	cred, err := fetcher.Fetch("agent-1", "api.example.com", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if cred != nil {
		t.Fatalf("Fetch() = %#v, want nil", cred)
	}
}

func TestResolverCachesByKeyAndTTL(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(resolveResponse{
			Credential: &Credential{
				HeaderName:   "Authorization",
				HeaderPrefix: "Bearer ",
				Value:        "secret",
			},
		})
	}))
	defer server.Close()

	resolver := NewResolver(server.URL)
	resolver.ttl = 40 * time.Millisecond

	for i := 0; i < 2; i++ {
		cred, err := resolver.Resolve("agent-1", "api.example.com", "user-1")
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if cred == nil || cred.Value != "secret" {
			t.Fatalf("Resolve() = %#v, want populated credential", cred)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls after cache hit = %d, want 1", got)
	}

	time.Sleep(60 * time.Millisecond)

	if _, err := resolver.Resolve("agent-1", "api.example.com", "user-1"); err != nil {
		t.Fatalf("Resolve() after ttl error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls after ttl expiry = %d, want 2", got)
	}
}

func TestResolverSeparatesUserIDCacheKeys(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req resolveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(resolveResponse{
			Credential: &Credential{
				HeaderName: "X-User",
				Value:      req.UserID,
			},
		})
	}))
	defer server.Close()

	resolver := NewResolver(server.URL)
	first, err := resolver.Resolve("agent-1", "api.example.com", "user-a")
	if err != nil {
		t.Fatalf("Resolve(user-a) error = %v", err)
	}
	second, err := resolver.Resolve("agent-1", "api.example.com", "user-b")
	if err != nil {
		t.Fatalf("Resolve(user-b) error = %v", err)
	}

	if first == nil || first.Value != "user-a" {
		t.Fatalf("first Resolve() = %#v, want user-a", first)
	}
	if second == nil || second.Value != "user-b" {
		t.Fatalf("second Resolve() = %#v, want user-b", second)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}
