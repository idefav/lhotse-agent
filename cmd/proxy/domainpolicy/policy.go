package domainpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"

	ModeDefaultAllow = "default_allow"
	ModeDefaultDeny  = "default_deny"
)

var errUnsupportedMode = errors.New("unsupported domain policy mode")

type ConfigDocument struct {
	Rules []RuleDocument `json:"rules"`
}

type RuleDocument struct {
	Direction string   `json:"direction"`
	Mode      string   `json:"mode"`
	AllowList []string `json:"allowList"`
	BlockList []string `json:"blockList"`
}

type Policy struct {
	rules map[string]*directionPolicy
}

type directionPolicy struct {
	mode  string
	allow matcherSet
	block matcherSet
}

type matcherSet struct {
	domains         map[string]struct{}
	wildcardDomains []string
	ips             []net.IP
	nets            []*net.IPNet
}

type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func Parse(data []byte) (*Policy, error) {
	var doc ConfigDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return Compile(doc)
}

func Compile(doc ConfigDocument) (*Policy, error) {
	p := &Policy{rules: map[string]*directionPolicy{}}
	for _, rule := range doc.Rules {
		direction := strings.ToLower(strings.TrimSpace(rule.Direction))
		if direction != DirectionInbound && direction != DirectionOutbound {
			return nil, fmt.Errorf("unsupported domain policy direction %q", rule.Direction)
		}
		mode := strings.ToLower(strings.TrimSpace(rule.Mode))
		if mode == "" {
			mode = ModeDefaultAllow
		}
		if mode != ModeDefaultAllow && mode != ModeDefaultDeny {
			return nil, fmt.Errorf("%w: %s", errUnsupportedMode, rule.Mode)
		}
		allow, err := compileMatcherSet(rule.AllowList)
		if err != nil {
			return nil, fmt.Errorf("%s allowList: %w", direction, err)
		}
		block, err := compileMatcherSet(rule.BlockList)
		if err != nil {
			return nil, fmt.Errorf("%s blockList: %w", direction, err)
		}
		p.rules[direction] = &directionPolicy{
			mode:  mode,
			allow: allow,
			block: block,
		}
	}
	return p, nil
}

func (p *Policy) Evaluate(direction, domain, targetAddr string) Decision {
	if p == nil {
		return Decision{Allowed: true}
	}
	rule := p.rules[direction]
	if rule == nil {
		return Decision{Allowed: true}
	}

	normalizedDomain := normalizeDomain(domain)
	targetIP := extractIP(targetAddr)
	switch rule.mode {
	case ModeDefaultAllow:
		if rule.block.matches(normalizedDomain, targetIP) {
			return Decision{Allowed: false, Reason: "domain_policy_blocklist"}
		}
		return Decision{Allowed: true}
	case ModeDefaultDeny:
		if rule.allow.matches(normalizedDomain, targetIP) {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "domain_policy_allowlist"}
	default:
		return Decision{Allowed: true}
	}
}

func (p *Policy) Summary() map[string]RuleSummary {
	result := map[string]RuleSummary{}
	if p == nil {
		return result
	}
	for direction, rule := range p.rules {
		result[direction] = RuleSummary{
			Mode:       rule.mode,
			AllowCount: rule.allow.count(),
			BlockCount: rule.block.count(),
		}
	}
	return result
}

type RuleSummary struct {
	Mode       string `json:"mode"`
	AllowCount int    `json:"allowCount"`
	BlockCount int    `json:"blockCount"`
}

func compileMatcherSet(values []string) (matcherSet, error) {
	set := matcherSet{domains: map[string]struct{}{}}
	for _, rawValue := range values {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		if _, ipNet, err := net.ParseCIDR(value); err == nil {
			set.nets = append(set.nets, ipNet)
			continue
		}
		if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
			set.ips = append(set.ips, ip)
			continue
		}
		domain := normalizeDomain(value)
		if domain == "" {
			continue
		}
		if strings.HasPrefix(domain, "*.") {
			suffix := strings.TrimPrefix(domain, "*.")
			if suffix == "" {
				return set, fmt.Errorf("invalid wildcard domain %q", rawValue)
			}
			set.wildcardDomains = append(set.wildcardDomains, suffix)
			continue
		}
		set.domains[domain] = struct{}{}
	}
	return set, nil
}

func (m matcherSet) matches(domain string, ip net.IP) bool {
	if domain != "" {
		if _, ok := m.domains[domain]; ok {
			return true
		}
		for _, suffix := range m.wildcardDomains {
			if strings.HasSuffix(domain, "."+suffix) && domain != suffix {
				return true
			}
		}
	}
	if ip != nil {
		for _, item := range m.ips {
			if item.Equal(ip) {
				return true
			}
		}
		for _, network := range m.nets {
			if network.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (m matcherSet) count() int {
	return len(m.domains) + len(m.wildcardDomains) + len(m.ips) + len(m.nets)
}

func normalizeDomain(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func extractIP(value string) net.IP {
	host := strings.TrimSpace(value)
	if host == "" {
		return nil
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return net.ParseIP(host)
}
