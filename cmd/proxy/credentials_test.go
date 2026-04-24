package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/proxy/runtimeconfig"
	"lhotse-agent/pkg/credential"
	"lhotse-agent/pkg/identity"
)

func TestResolveUserIDFallsBackToTrustedHeader(t *testing.T) {
	o := &OutboundServer{
		RuntimeConfig: runtimeConfigForTest(runtimeconfig.Document{
			UserSources: []runtimeconfig.UserSource{
				{Type: runtimeconfig.UserSourceOAuth2Token, Header: "Authorization", Prefix: "Bearer ", Provider: "oidc"},
				{Type: runtimeconfig.UserSourceHeader, Header: "X-User-ID"},
			},
		}),
		IdentityResolver: identity.NewResolver(""),
	}

	got, err := o.resolveUserID(func(key string) string {
		if key == "X-User-ID" {
			return "user-123"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveUserID() error = %v", err)
	}
	if got != "user-123" {
		t.Fatalf("resolveUserID() = %q, want %q", got, "user-123")
	}
}

func TestResolveUserIDWrongPrefixFallsBackToTrustedHeader(t *testing.T) {
	o := &OutboundServer{
		RuntimeConfig: runtimeConfigForTest(runtimeconfig.Document{
			UserSources: []runtimeconfig.UserSource{
				{Type: runtimeconfig.UserSourceOAuth2Token, Header: "Authorization", Prefix: "Bearer ", Provider: "oidc"},
				{Type: runtimeconfig.UserSourceHeader, Header: "X-User-ID"},
			},
		}),
		IdentityResolver: identity.NewResolver(""),
	}

	got, err := o.resolveUserID(func(key string) string {
		switch key {
		case "Authorization":
			return "Token abc"
		case "X-User-ID":
			return "user-123"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("resolveUserID() error = %v", err)
	}
	if got != "user-123" {
		t.Fatalf("resolveUserID() = %q, want %q", got, "user-123")
	}
}

func TestResolveUserIDEmptyIdentityFallsBackToTrustedHeader(t *testing.T) {
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/identity/resolve":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"","ttl_seconds":60}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer vault.Close()

	o := &OutboundServer{
		RuntimeConfig: runtimeConfigForTest(runtimeconfig.Document{
			UserSources: []runtimeconfig.UserSource{
				{Type: runtimeconfig.UserSourceOAuth2Token, Header: "Authorization", Prefix: "Bearer ", Provider: "oidc"},
				{Type: runtimeconfig.UserSourceHeader, Header: "X-User-ID"},
			},
		}),
		IdentityResolver: identity.NewResolver(vault.URL),
	}

	got, err := o.resolveUserID(func(key string) string {
		switch key {
		case "Authorization":
			return "Bearer opaque-token"
		case "X-User-ID":
			return "user-456"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("resolveUserID() error = %v", err)
	}
	if got != "user-456" {
		t.Fatalf("resolveUserID() = %q, want %q", got, "user-456")
	}
}

func TestResolveUserIDIdentityBackendErrorFails(t *testing.T) {
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer vault.Close()

	o := &OutboundServer{
		RuntimeConfig: runtimeConfigForTest(runtimeconfig.Document{
			UserSources: []runtimeconfig.UserSource{
				{Type: runtimeconfig.UserSourceOAuth2Token, Header: "Authorization", Prefix: "Bearer ", Provider: "oidc"},
				{Type: runtimeconfig.UserSourceHeader, Header: "X-User-ID"},
			},
		}),
		IdentityResolver: identity.NewResolver(vault.URL),
	}

	_, err := o.resolveUserID(func(key string) string {
		switch key {
		case "Authorization":
			return "Bearer opaque-token"
		case "X-User-ID":
			return "user-123"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("resolveUserID() error = nil, want non-nil")
	}
}

func TestApplyCredentialInjectionUsesResolvedUserID(t *testing.T) {
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/identity/resolve":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-123","ttl_seconds":60}`))
		case "/internal/credentials/resolve":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"credential":{"header_name":"Authorization","header_prefix":"Bearer ","value":"secret"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer vault.Close()

	headers := http.Header{
		"Authorization": []string{"Bearer opaque-token"},
	}
	o := &OutboundServer{
		Cfg: &config.Config{AgentID: "agent-1"},
		RuntimeConfig: runtimeConfigForTest(runtimeconfig.Document{
			UserSources: []runtimeconfig.UserSource{
				{Type: runtimeconfig.UserSourceOAuth2Token, Header: "Authorization", Prefix: "Bearer ", Provider: "oidc"},
			},
		}),
		IdentityResolver: identity.NewResolver(vault.URL),
		CredResolver:     credential.NewResolver(vault.URL),
	}

	userID, err := o.applyCredentialInjection("api.example.com", headers.Get, headers.Set)
	if err != nil {
		t.Fatalf("applyCredentialInjection() error = %v", err)
	}
	if userID != "user-123" {
		t.Fatalf("userID = %q, want %q", userID, "user-123")
	}
	if got := headers.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer secret")
	}
}

func runtimeConfigForTest(doc runtimeconfig.Document) *runtimeconfig.Manager {
	manager := runtimeconfig.NewManager(runtimeconfig.Options{})
	if err := manager.ReloadDocumentForTest(doc); err != nil {
		panic(err)
	}
	return manager
}
