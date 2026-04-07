package proxy

import (
	"bufio"
	"net"
	"strconv"
	"strings"

	lhotseLog "lhotse-agent/pkg/log"
	lhotseHttp "lhotse-agent/pkg/protocol/http"
	lhotseTLS "lhotse-agent/pkg/protocol/tls"
)

const (
	protoTCP     = "tcp"
	protoTLS     = "tls"
	protoHTTP    = "http"
	protoCONNECT = "connect"
	protoWS      = "websocket"
)

type trafficMetadata struct {
	direction  string
	proto      string
	domain     string
	targetIP   string
	targetPort string
	remoteAddr string
	localAddr  string
}

func newTrafficMetadata(direction string, conn net.Conn, targetAddr string) trafficMetadata {
	host, port := splitHostPort(targetAddr)
	return trafficMetadata{
		direction:  direction,
		proto:      protoTCP,
		domain:     "-",
		targetIP:   host,
		targetPort: port,
		remoteAddr: conn.RemoteAddr().String(),
		localAddr:  conn.LocalAddr().String(),
	}
}

func (m trafficMetadata) withProto(proto string) trafficMetadata {
	m.proto = proto
	return m
}

func (m trafficMetadata) withDomain(domain string) trafficMetadata {
	if domain != "" {
		m.domain = domain
	}
	return m
}

func (m trafficMetadata) log() {
	lhotseLog.Infof(
		"traffic direction=%s proto=%s domain=%s target_ip=%s target_port=%s remote_addr=%s local_addr=%s",
		m.direction,
		m.proto,
		m.domain,
		m.targetIP,
		m.targetPort,
		m.remoteAddr,
		m.localAddr,
	)
}

func logHTTPRequest(direction string, conn net.Conn, targetAddr string, request *lhotseHttp.Request) {
	meta := newTrafficMetadata(direction, conn, targetAddr).withProto(protoHTTP).withDomain(request.TargetDomain())
	if request.Method == "CONNECT" {
		meta = meta.withProto(protoCONNECT)
	}
	if request.Port > 0 {
		meta.targetPort = strconv.Itoa(int(request.Port))
	}
	meta.log()
}

func logTCPConnection(direction string, conn net.Conn, targetAddr string) {
	newTrafficMetadata(direction, conn, targetAddr).log()
}

func logTLSConnection(direction string, conn net.Conn, targetAddr string, reader *bufio.Reader) {
	meta := newTrafficMetadata(direction, conn, targetAddr)
	serverName, isTLS, err := lhotseTLS.PeekClientHelloServerName(reader)
	if err == nil && isTLS {
		meta = meta.withProto(protoTLS).withDomain(serverName)
	}
	meta.log()
}

func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		return host, port
	}
	return addr, "-"
}

func logUpgradedConnection(direction string, conn net.Conn, targetAddr string, request *lhotseHttp.Request, upgradeType string) {
	meta := newTrafficMetadata(direction, conn, targetAddr).withDomain(request.TargetDomain())
	if strings.EqualFold(upgradeType, "websocket") {
		meta = meta.withProto(protoWS)
	} else {
		meta = meta.withProto(upgradeType)
	}
	if request.Port > 0 {
		meta.targetPort = strconv.Itoa(int(request.Port))
	}
	meta.log()
}
