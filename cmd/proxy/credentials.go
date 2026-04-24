package proxy

import (
	"fmt"
	"net"
	"strings"

	"lhotse-agent/cmd/proxy/runtimeconfig"
)

func (o *OutboundServer) applyCredentialInjection(targetHost string, getHeader func(string) string, setHeader func(string, string)) (string, error) {
	if o.CredResolver == nil {
		return "", nil
	}
	targetHost = normalizeHost(targetHost)
	userID, err := o.resolveUserID(getHeader)
	if err != nil {
		return "", err
	}
	cred, err := o.CredResolver.Resolve(o.Cfg.AgentID, targetHost, userID)
	if err != nil {
		return userID, err
	}
	if cred != nil {
		setHeader(cred.HeaderName, cred.HeaderPrefix+cred.Value)
	}
	return userID, nil
}

func (o *OutboundServer) resolveUserID(getHeader func(string) string) (string, error) {
	if o.RuntimeConfig == nil || o.IdentityResolver == nil {
		return "", nil
	}
	for _, source := range o.RuntimeConfig.UserSources() {
		switch source.Type {
		case runtimeconfig.UserSourceOAuth2Token:
			raw := strings.TrimSpace(getHeader(source.Header))
			if raw == "" {
				continue
			}
			token, err := trimTokenPrefix(raw, source.Prefix)
			if err != nil {
				continue
			}
			userID, err := o.IdentityResolver.Resolve(source.Provider, token)
			if err != nil {
				return "", err
			}
			if userID != "" {
				return userID, nil
			}
			continue
		case runtimeconfig.UserSourceHeader:
			raw := strings.TrimSpace(getHeader(source.Header))
			if raw != "" {
				return raw, nil
			}
		}
	}
	return "", nil
}

func trimTokenPrefix(value, prefix string) (string, error) {
	value = strings.TrimSpace(value)
	if prefix == "" {
		return value, nil
	}
	prefix = strings.TrimSpace(prefix)
	if !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
		return "", fmt.Errorf("identity header missing expected prefix %q", prefix)
	}
	token := strings.TrimSpace(value[len(prefix):])
	if token == "" {
		return "", fmt.Errorf("identity header token is empty")
	}
	return token, nil
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

func extractHost(addr string) string {
	return normalizeHost(addr)
}
