package runtimeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	UserSourceOAuth2Token = "oauth2_token"
	UserSourceHeader      = "trusted_header"
)

type UserSource struct {
	Type     string `json:"type"`
	Header   string `json:"header"`
	Prefix   string `json:"prefix,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type Document struct {
	MITMHosts   []string     `json:"mitm_hosts"`
	UserSources []UserSource `json:"user_sources"`
}

type Options struct {
	URL             string
	CacheFile       string
	RefreshInterval time.Duration
	FetchTimeout    time.Duration
	AppName         string
	InstanceIP      string
	AgentID         string
}

type Status struct {
	Enabled         bool     `json:"enabled"`
	URL             string   `json:"url,omitempty"`
	CacheFile       string   `json:"cacheFile,omitempty"`
	RefreshInterval string   `json:"refreshInterval,omitempty"`
	AppName         string   `json:"appName,omitempty"`
	InstanceIP      string   `json:"instanceIP,omitempty"`
	AgentID         string   `json:"agentID,omitempty"`
	Source          string   `json:"source,omitempty"`
	LastRefresh     string   `json:"lastRefresh,omitempty"`
	LastError       string   `json:"lastError,omitempty"`
	MITMHosts       []string `json:"mitmHosts,omitempty"`
	UserSources     int      `json:"userSources"`
}

type compiled struct {
	doc       Document
	exact     map[string]struct{}
	wildcards []string
}

type Manager struct {
	options Options
	client  *http.Client

	mu          sync.RWMutex
	cfg         *compiled
	source      string
	lastRefresh time.Time
	lastError   string
	stop        chan struct{}
}

func NewManager(options Options) *Manager {
	if options.FetchTimeout <= 0 {
		options.FetchTimeout = 5 * time.Second
	}
	if options.CacheFile == "" {
		options.CacheFile = "/tmp/lhotse-credential-runtime-cache.json"
	}
	return &Manager{
		options: options,
		client:  &http.Client{Timeout: options.FetchTimeout},
		stop:    make(chan struct{}),
	}
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
		log.Warnf("credential runtime cache load failed: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		log.Warnf("credential runtime remote fetch failed: %v", err)
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
	if strings.TrimSpace(m.options.AgentID) == "" {
		err := fmt.Errorf("agent-id is required when mitm-enabled is true")
		m.setError(err.Error())
		return err
	}
	body, err := m.fetch(ctx)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	cfg, err := parse(body)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	m.setConfig(cfg, "remote", "")
	if err := m.saveCache(body); err != nil {
		log.Warnf("credential runtime cache save failed: %v", err)
	}
	return nil
}

func (m *Manager) MITMEnabledForHost(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg == nil {
		return false
	}
	if _, ok := m.cfg.exact[host]; ok {
		return true
	}
	for _, suffix := range m.cfg.wildcards {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (m *Manager) UserSources() []UserSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg == nil {
		return nil
	}
	out := make([]UserSource, len(m.cfg.doc.UserSources))
	copy(out, m.cfg.doc.UserSources)
	return out
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{
		Enabled:         m.Enabled(),
		URL:             m.options.URL,
		CacheFile:       m.options.CacheFile,
		RefreshInterval: m.options.RefreshInterval.String(),
		AppName:         m.options.AppName,
		InstanceIP:      m.options.InstanceIP,
		AgentID:         m.options.AgentID,
		Source:          m.source,
		LastError:       m.lastError,
	}
	if !m.lastRefresh.IsZero() {
		status.LastRefresh = m.lastRefresh.Format(time.RFC3339)
	}
	if m.cfg != nil {
		status.MITMHosts = append([]string(nil), m.cfg.doc.MITMHosts...)
		status.UserSources = len(m.cfg.doc.UserSources)
	}
	return status
}

func (m *Manager) refreshLoop() {
	ticker := time.NewTicker(m.options.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := m.Reload(context.Background()); err != nil {
				log.Warnf("credential runtime periodic refresh failed: %v", err)
			}
		case <-m.stop:
			return
		}
	}
}

func (m *Manager) fetch(ctx context.Context) ([]byte, error) {
	endpoint, err := url.Parse(strings.TrimRight(m.options.URL, "/") + "/internal/credential-runtime-config")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	if m.options.AppName != "" {
		query.Set("app", m.options.AppName)
	}
	if m.options.InstanceIP != "" {
		query.Set("ip", m.options.InstanceIP)
	}
	query.Set("agent_id", m.options.AgentID)
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
		return nil, fmt.Errorf("credential runtime fetch failed: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (m *Manager) loadCache() error {
	if m.options.CacheFile == "" {
		return nil
	}
	body, err := os.ReadFile(m.options.CacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cfg, err := parse(body)
	if err != nil {
		return err
	}
	m.setConfig(cfg, "cache", "")
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
	tmp, err := os.CreateTemp(dir, ".credential-runtime-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.options.CacheFile)
}

func (m *Manager) setConfig(cfg *compiled, source, lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.source = source
	m.lastRefresh = time.Now()
	m.lastError = lastError
}

func (m *Manager) setError(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = msg
}

func parse(body []byte) (*compiled, error) {
	var doc Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	cfg := &compiled{
		doc:   doc,
		exact: map[string]struct{}{},
	}
	for _, raw := range doc.MITMHosts {
		host := normalizeHost(raw)
		if host == "" {
			continue
		}
		if strings.HasPrefix(host, "*.") {
			cfg.wildcards = append(cfg.wildcards, strings.TrimPrefix(host, "*."))
			continue
		}
		cfg.exact[host] = struct{}{}
	}
	return cfg, nil
}

func (m *Manager) ReloadDocumentForTest(doc Document) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	cfg, err := parse(body)
	if err != nil {
		return err
	}
	m.setConfig(cfg, "test", "")
	return nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func localIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || localAddr.IP == nil {
		return "", fmt.Errorf("no local ip")
	}
	return localAddr.IP.String(), nil
}
