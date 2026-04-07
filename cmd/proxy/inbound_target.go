package proxy

import "net"

func inboundDialTarget(originalTarget string) string {
	_, port, err := net.SplitHostPort(originalTarget)
	if err != nil {
		return originalTarget
	}
	return net.JoinHostPort("127.0.0.1", port)
}
