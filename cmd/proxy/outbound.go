package proxy

import (
	"bufio"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/socket"
	"lhotse-agent/util"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func (o *OutboundServer) Startup() error {
	ln, err := upgrade.Upgrade.Listen("tcp", ":"+strconv.Itoa(int(o.Port)))
	if err != nil {
		return err
	}
	util.GO(func() {
		o.proc(ln)
	})
	return nil
}

func (o *OutboundServer) proc(ln net.Listener) error {
	for {
		conn, _ := ln.Accept()

		//log.Println("接收到新Http请求", err2)
		util.GO(func() {
			defer conn.Close()
			atomic.AddInt32(&o.NumOpen, 1)
			defer atomic.AddInt32(&o.NumOpen, -1)

			var dst_host = ""
			_, host, _, err := socket.GetOriginalDst(conn.(*net.TCPConn))
			if err == nil {
				dst_host = host
			}

			for {
				//log.Println("准备读取")
				conn.SetReadDeadline(time.Now().Add(o.IdleTimeOut))
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
					}
					logTCPConnection("outbound", conn, dst_host)
					if err := o.HttpProc(conn, reader, dst_host); err != nil {
						return
					}
				} else {
					if dst_host == "" {
						dst_host = "192.168.0.105:28081"
					}
					if err := enforceRawDomainPolicy(o.Cfg, outboundDirection(), conn, reader, dst_host); err != nil {
						return
					}
					logTLSConnection("outbound", conn, dst_host, reader)
					destConn, err := net.Dial("tcp", dst_host)
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

func (o *OutboundServer) Shutdown() error {
	for o.NumOpen > 0 {
		time.Sleep(time.Second)
		continue
	}
	return nil
}
