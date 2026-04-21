package proxy

import (
	"bufio"
	"fmt"
	"net"
	"strconv"

	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/proxy/domainpolicy"
	lhotseLog "lhotse-agent/pkg/log"
	lhotseTLS "lhotse-agent/pkg/protocol/tls"
)

func enforceDomainPolicy(cfg *config.Config, direction string, downstream net.Conn, domain string, targetAddr string) error {
	if cfg == nil || cfg.DomainPolicy == nil {
		return nil
	}
	decision := cfg.DomainPolicy.Evaluate(direction, domain, targetAddr)
	if decision.Allowed {
		return nil
	}
	lhotseLog.Warnf("domain policy denied direction=%s domain=%s target=%s reason=%s", direction, domain, targetAddr, decision.Reason)
	if downstream != nil {
		_ = writeDomainPolicyForbidden(downstream, decision.Reason)
	}
	return errProxyConnectionDone
}

func enforceRawDomainPolicy(cfg *config.Config, direction string, downstream net.Conn, reader *bufio.Reader, targetAddr string) error {
	domain := ""
	serverName, isTLS, err := lhotseTLS.PeekClientHelloServerName(reader)
	if err == nil && isTLS {
		domain = serverName
	}
	return enforceDomainPolicy(cfg, direction, downstream, domain, targetAddr)
}

func writeDomainPolicyForbidden(downstream net.Conn, reason string) error {
	if reason == "" {
		reason = "domain_policy_denied"
	}
	body := fmt.Sprintf("Forbidden: %s\n", reason)
	response := "HTTP/1.1 403 Forbidden\r\n" +
		"Server: lhotse-agent\r\n" +
		"Content-Type: text/plain;charset=UTF-8\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"Connection: close\r\n\r\n" +
		body
	_, err := downstream.Write([]byte(response))
	return err
}

func outboundDirection() string {
	return domainpolicy.DirectionOutbound
}

func inboundDirection() string {
	return domainpolicy.DirectionInbound
}
