package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Identity struct {
	UserID string
	TTL    time.Duration
}

type resolveRequest struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
}

type resolveResponse struct {
	UserID     string `json:"user_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type Resolver struct {
	vaultURI string
	client   *http.Client
	cache    sync.Map
}

type cacheEntry struct {
	userID    string
	expiresAt time.Time
}

func NewResolver(vaultURI string) *Resolver {
	return &Resolver{
		vaultURI: strings.TrimRight(vaultURI, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (r *Resolver) Resolve(provider, token string) (string, error) {
	if r.vaultURI == "" || provider == "" || token == "" {
		return "", nil
	}
	key := cacheKey(provider, token)
	if val, ok := r.cache.Load(key); ok {
		entry := val.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.userID, nil
		}
		r.cache.Delete(key)
	}

	body, err := json.Marshal(resolveRequest{
		Provider: provider,
		Token:    token,
	})
	if err != nil {
		return "", err
	}
	resp, err := r.client.Post(r.vaultURI+"/internal/identity/resolve", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("identity resolve returned %d: %s", resp.StatusCode, string(payload))
	}

	var payload resolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	ttl := 30 * time.Second
	if payload.TTLSeconds > 0 {
		ttl = time.Duration(payload.TTLSeconds) * time.Second
	}
	r.cache.Store(key, &cacheEntry{
		userID:    payload.UserID,
		expiresAt: time.Now().Add(ttl),
	})
	return payload.UserID, nil
}

func cacheKey(provider, token string) string {
	sum := sha256.Sum256([]byte(token))
	return provider + ":" + hex.EncodeToString(sum[:])
}
