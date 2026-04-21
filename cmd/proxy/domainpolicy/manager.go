package domainpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lhotse-agent/pkg/log"
	"lhotse-agent/util"
)

const (
	ScopeInbound  = "inbound"
	ScopeOutbound = "outbound"
	ScopeBoth     = "both"
)

type Options struct {
	URL             string
	CacheFile       string
	RefreshInterval time.Duration
	FetchTimeout    time.Duration
	Scope           string
	AppName         string
	InstanceIP      string
}

type Manager struct {
	options Options
	client  *http.Client

	mu          sync.RWMutex
	policy      *Policy
	source      string
	lastRefresh time.Time
	lastError   string
	stop        chan struct{}
}

func NewManager(options Options) *Manager {
	if options.Scope == "" {
		options.Scope = ScopeOutbound
	}
	if options.FetchTimeout <= 0 {
		options.FetchTimeout = 5 * time.Second
	}
	if options.CacheFile == "" {
		options.CacheFile = "/tmp/lhotse-domain-policy-cache.json"
	}
	return &Manager{
		options: options,
		client:  &http.Client{Timeout: options.FetchTimeout},
		stop:    make(chan struct{}),
	}
}

func NewStaticManager(policy *Policy, scope string) *Manager {
	manager := NewManager(Options{URL: "static", Scope: scope})
	manager.setPolicy(policy, "static", "")
	return manager
}

func (m *Manager) Enabled() bool {
	return strings.TrimSpace(m.options.URL) != ""
}

func (m *Manager) Start() {
	if !m.Enabled() {
		return
	}
	if m.options.InstanceIP == "" {
		if ip, err := localIP(); err == nil {
			m.options.InstanceIP = ip
		} else {
			m.setError(fmt.Sprintf("instance ip lookup failed: %v", err))
		}
	}
	if err := m.loadCache(); err != nil {
		log.Warnf("domain policy cache load failed: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		log.Warnf("domain policy remote fetch failed: %v", err)
	}
	if m.options.RefreshInterval > 0 {
		util.GO(m.refreshLoop)
	}
}

func (m *Manager) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

func (m *Manager) Reload(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}
	if strings.TrimSpace(m.options.AppName) == "" {
		err := fmt.Errorf("app-name is required when domain-policy-url is configured")
		m.setError(err.Error())
		return err
	}
	if strings.TrimSpace(m.options.InstanceIP) == "" {
		err := fmt.Errorf("instance-ip is required when domain-policy-url is configured")
		m.setError(err.Error())
		return err
	}

	body, err := m.fetch(ctx)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	policy, err := Parse(body)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	m.setPolicy(policy, "remote", "")
	if err := m.saveCache(body); err != nil {
		log.Warnf("domain policy cache save failed: %v", err)
	}
	return nil
}

func (m *Manager) Evaluate(direction, domain, targetAddr string) Decision {
	if !m.Enabled() || !m.directionEnabled(direction) {
		return Decision{Allowed: true}
	}
	m.mu.RLock()
	policy := m.policy
	m.mu.RUnlock()
	if policy == nil {
		return Decision{Allowed: true}
	}
	return policy.Evaluate(direction, domain, targetAddr)
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{
		Enabled:         m.Enabled(),
		URL:             m.options.URL,
		CacheFile:       m.options.CacheFile,
		RefreshInterval: m.options.RefreshInterval.String(),
		Scope:           m.options.Scope,
		AppName:         m.options.AppName,
		InstanceIP:      m.options.InstanceIP,
		Source:          m.source,
		LastError:       m.lastError,
		Rules:           map[string]RuleSummary{},
	}
	if !m.lastRefresh.IsZero() {
		status.LastRefresh = m.lastRefresh.Format(time.RFC3339)
	}
	if m.policy != nil {
		status.Rules = m.policy.Summary()
	}
	return status
}

type Status struct {
	Enabled         bool                   `json:"enabled"`
	URL             string                 `json:"url,omitempty"`
	CacheFile       string                 `json:"cacheFile,omitempty"`
	RefreshInterval string                 `json:"refreshInterval,omitempty"`
	Scope           string                 `json:"scope,omitempty"`
	AppName         string                 `json:"appName,omitempty"`
	InstanceIP      string                 `json:"instanceIP,omitempty"`
	Source          string                 `json:"source,omitempty"`
	LastRefresh     string                 `json:"lastRefresh,omitempty"`
	LastError       string                 `json:"lastError,omitempty"`
	Rules           map[string]RuleSummary `json:"rules"`
}

func (m *Manager) refreshLoop() {
	ticker := time.NewTicker(m.options.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := m.Reload(context.Background()); err != nil {
				log.Warnf("domain policy periodic refresh failed: %v", err)
			}
		case <-m.stop:
			return
		}
	}
}

func (m *Manager) fetch(ctx context.Context) ([]byte, error) {
	endpoint, err := url.Parse(m.options.URL)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("app", m.options.AppName)
	query.Set("ip", m.options.InstanceIP)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("domain policy fetch failed: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (m *Manager) loadCache() error {
	if m.options.CacheFile == "" {
		return nil
	}
	body, err := ioutil.ReadFile(m.options.CacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	policy, err := Parse(body)
	if err != nil {
		return err
	}
	m.setPolicy(policy, "cache", "")
	return nil
}

func (m *Manager) saveCache(body []byte) error {
	if m.options.CacheFile == "" {
		return nil
	}
	dir := filepath.Dir(m.options.CacheFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := ioutil.TempFile(dir, ".domain-policy-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err = tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, m.options.CacheFile)
}

func (m *Manager) setPolicy(policy *Policy, source, lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = policy
	m.source = source
	m.lastRefresh = time.Now()
	m.lastError = lastError
}

func (m *Manager) setError(lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = lastError
}

func (m *Manager) directionEnabled(direction string) bool {
	switch m.options.Scope {
	case ScopeBoth:
		return true
	case ScopeInbound:
		return direction == DirectionInbound
	case ScopeOutbound, "":
		return direction == DirectionOutbound
	default:
		return direction == DirectionOutbound
	}
}

func localIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		return ip.String(), nil
	}
	return "", fmt.Errorf("no valid local IP address found")
}

func WriteStatus(w http.ResponseWriter, status Status) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
