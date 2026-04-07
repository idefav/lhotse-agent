//go:build !linux
// +build !linux

package socket

import (
	"fmt"
	"net"
)

func EnableUDPSocketOptions(fd int) error {
	return fmt.Errorf("udp transparent proxy requires linux")
}

func EnableTransparentSocketOptions(fd int) error {
	return fmt.Errorf("udp transparent proxy requires linux")
}

func ParseOriginalDstFromOOB(oob []byte) (*net.UDPAddr, error) {
	return nil, fmt.Errorf("udp transparent proxy requires linux")
}
