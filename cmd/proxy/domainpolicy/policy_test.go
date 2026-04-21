package domainpolicy

import "testing"

func TestPolicyDirectionalDomainAndIPRules(t *testing.T) {
	policy, err := Compile(ConfigDocument{Rules: []RuleDocument{
		{
			Direction: DirectionOutbound,
			Mode:      ModeDefaultAllow,
			BlockList: []string{"*.blocked.example.com", "198.51.100.0/24"},
		},
		{
			Direction: DirectionInbound,
			Mode:      ModeDefaultDeny,
			AllowList: []string{"admin.example.com", "192.0.2.10"},
		},
	}})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	tests := []struct {
		name      string
		direction string
		domain    string
		target    string
		allowed   bool
	}{
		{
			name:      "outbound blocklist wildcard denies subdomain",
			direction: DirectionOutbound,
			domain:    "api.blocked.example.com",
			target:    "203.0.113.10:443",
			allowed:   false,
		},
		{
			name:      "outbound wildcard does not deny bare domain",
			direction: DirectionOutbound,
			domain:    "blocked.example.com",
			target:    "203.0.113.10:443",
			allowed:   true,
		},
		{
			name:      "outbound cidr denies raw tcp by ip",
			direction: DirectionOutbound,
			domain:    "",
			target:    "198.51.100.25:443",
			allowed:   false,
		},
		{
			name:      "inbound allowlist allows exact domain",
			direction: DirectionInbound,
			domain:    "ADMIN.EXAMPLE.COM.",
			target:    "203.0.113.20:443",
			allowed:   true,
		},
		{
			name:      "inbound allowlist allows exact ip",
			direction: DirectionInbound,
			domain:    "",
			target:    "192.0.2.10:8443",
			allowed:   true,
		},
		{
			name:      "inbound allowlist denies miss",
			direction: DirectionInbound,
			domain:    "www.example.com",
			target:    "203.0.113.20:443",
			allowed:   false,
		},
		{
			name:      "missing direction allows",
			direction: "unknown",
			domain:    "www.example.com",
			target:    "203.0.113.20:443",
			allowed:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Evaluate(tt.direction, tt.domain, tt.target)
			if decision.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v, want %v, reason=%s", decision.Allowed, tt.allowed, decision.Reason)
			}
		})
	}
}

func TestPolicySupportsIPv6CIDR(t *testing.T) {
	policy, err := Compile(ConfigDocument{Rules: []RuleDocument{
		{
			Direction: DirectionOutbound,
			Mode:      ModeDefaultDeny,
			AllowList: []string{"2001:db8::/32"},
		},
	}})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	decision := policy.Evaluate(DirectionOutbound, "", "[2001:db8::1]:443")
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true")
	}
}
