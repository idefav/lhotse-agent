package credential

import (
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type Resolver struct {
	fetcher *Fetcher
	cache   sync.Map // key -> *cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	cred      *Credential
	expiresAt time.Time
}

func NewResolver(vaultURI string) *Resolver {
	return &Resolver{
		fetcher: NewFetcher(vaultURI),
		ttl:     30 * time.Second,
	}
}

func (r *Resolver) Resolve(agentID, targetHost, userID string) (*Credential, error) {
	if r.fetcher.vaultURI == "" {
		return nil, nil
	}

	key := fmtKey(agentID, targetHost, userID)
	if val, ok := r.cache.Load(key); ok {
		entry := val.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.cred, nil
		}
		r.cache.Delete(key)
	}

	cred, err := r.fetcher.Fetch(agentID, targetHost, userID)
	if err != nil {
		klog.Errorf("Failed to resolve credential for %s: %v", key, err)
		return nil, err
	}

	r.cache.Store(key, &cacheEntry{
		cred:      cred,
		expiresAt: time.Now().Add(r.ttl),
	})

	return cred, nil
}

func fmtKey(agentID, targetHost, userID string) string {
	return agentID + ":" + targetHost + ":" + userID
}
