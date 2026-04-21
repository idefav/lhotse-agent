package proxy

import (
	"bufio"
	"fmt"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/socket"
	"lhotse-agent/util"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func generateKey(localAddr, remoteAddr string) string {
	return fmt.Sprintf("%s-%s", localAddr, remoteAddr)
}

func (inProxyServer *InProxyServer) AddConn(conn net.Conn) error {
	return nil
}

func (inProxyServer *InProxyServer) RemoveConn(conn net.Conn) error {
	return nil
}

func (inProxyServer *InProxyServer) Startup() error {
	ln, err := upgrade.Upgrade.Listen("tcp", ":"+strconv.Itoa(int(inProxyServer.Port)))
	if err != nil {
		return err
	}

	util.GO(func() {
		inProxyServer.proc(ln)
	})

	return nil
}

func (inProxyServer *InProxyServer) Shutdown() error {
	for inProxyServer.NumOpen > 0 {
		time.Sleep(time.Second)
		continue
	}
	return nil
}

var ConnC chan net.Conn

func (inProxyServer *InProxyServer) proc(ln net.Listener) error {
	for {
		conn, _ := ln.Accept()

		util.GO(func() {
			defer conn.Close()
			atomic.AddInt32(&inProxyServer.NumOpen, 1)
			defer atomic.AddInt32(&inProxyServer.NumOpen, -1)

			var dst_host = ""
			var dialTarget = ""
			_, host, _, err := socket.GetOriginalDst(conn.(*net.TCPConn))
			if err == nil {
				dst_host = host
				dialTarget = inboundDialTarget(host)
			}

			for {
				//log.Println("准备读取")
				conn.SetReadDeadline(time.Now().Add(inProxyServer.IdleTimeOut))
				reader := bufio.NewReader(conn)
				peek, err := reader.Peek(5)
				if err != nil {
					//log.Println("连接断开")
					return
				}
				header := string(peek)

				if strings.HasPrefix(header, "GET") || strings.HasPrefix(header, "POST") ||
					strings.HasPrefix(header, "HEAD") || strings.HasPrefix(header, "CONNECT") ||
					strings.HasPrefix(header, "OPTIONS") || strings.HasPrefix(header, "PUT") ||
					strings.HasPrefix(header, "DELETE") {
					if dst_host == "" {
						dst_host = "192.168.0.105:28080"
						dialTarget = dst_host
					}
					logTCPConnection("inbound", conn, dst_host)
					if err := inProxyServer.HttpProc2(conn, reader, dst_host, dialTarget); err != nil {
						return
					}
				} else {
					if dst_host == "" {
						dst_host = "192.168.0.105:28081"
						dialTarget = dst_host
					}
					if err := enforceRawDomainPolicy(inProxyServer.Cfg, inboundDirection(), conn, reader, dst_host); err != nil {
						return
					}
					logTLSConnection("inbound", conn, dst_host, reader)
					destConn, err := net.Dial("tcp", dialTarget)
					if err != nil {
						conn.Close()
						return
					}
					defer destConn.Close()
					if err := proxyRawConnection(conn, destConn, reader); err != nil {
						return
					}
					return
				}
			}

		})

	}
}
