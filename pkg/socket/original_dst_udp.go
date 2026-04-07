//go:build linux
// +build linux

package socket

import (
	"encoding/binary"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// EnableUDPSocketOptions enables original-destination metadata on redirected UDP sockets.
func EnableUDPSocketOptions(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_RECVORIGDSTADDR, 1); err != nil {
		return fmt.Errorf("set IP_RECVORIGDSTADDR: %w", err)
	}
	return nil
}

// EnableTransparentSocketOptions enables binding to non-local addresses for transparent proxy replies.
func EnableTransparentSocketOptions(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.SOL_IP, unix.IP_TRANSPARENT, 1); err != nil {
		return fmt.Errorf("set IP_TRANSPARENT: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_IP, unix.IP_FREEBIND, 1); err != nil {
		return fmt.Errorf("set IP_FREEBIND: %w", err)
	}
	return nil
}

// ParseOriginalDstFromOOB extracts the redirected UDP packet's original destination.
func ParseOriginalDstFromOOB(oob []byte) (*net.UDPAddr, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("parse control message: %w", err)
	}

	for _, msg := range msgs {
		if msg.Header.Level != unix.IPPROTO_IP || msg.Header.Type != unix.IP_ORIGDSTADDR {
			continue
		}
		if len(msg.Data) < 8 {
			return nil, fmt.Errorf("original dst control message too short: %d", len(msg.Data))
		}
		ip := net.IPv4(msg.Data[4], msg.Data[5], msg.Data[6], msg.Data[7])
		port := int(binary.BigEndian.Uint16(msg.Data[2:4]))
		return &net.UDPAddr{IP: ip, Port: port}, nil
	}

	return nil, fmt.Errorf("original destination not found")
}
