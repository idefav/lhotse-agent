package proxy

import (
	"bufio"
	"idefav-proxy/cmd/upgrade"
	"idefav-proxy/pkg/socket"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

func (o *OutboundServer) Startup() error {
	ln, err := upgrade.Upgrade.Listen("tcp", ":15001")
	if err != nil {
		return err
	}
	go o.proc(ln)
	return nil
}

func (o *OutboundServer) proc(ln net.Listener) error {
	for {
		conn, _ := ln.Accept()

		//log.Println("接收到新Http请求", err2)
		go func() {
			defer conn.Close()
			atomic.AddInt32(&o.NumOpen, 1)
			defer atomic.AddInt32(&o.NumOpen, -1)
			log.Printf("remoteAddr: %s --> localAddr: %s", conn.RemoteAddr(), conn.LocalAddr())

			var dst_host = ""
			dst, host, tcpConn, err := socket.GetOriginalDst(conn.(*net.TCPConn))
			log.Println(dst, host, tcpConn, err)
			if err == nil {
				dst_host = host
			}

			for {
				//log.Println("准备读取")
				conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
					//log.Println("开始Http协议解析")
					if dst_host == "" {
						dst_host = "192.168.0.105:28080"
					}
					o.HttpProc(conn, reader, dst_host)
				} else {
					//log.Println(header)
					//writer := bufio.NewWriter(conn)
					//var body = "收到!" + mgr.Version + "\n"
					//var respContent = "HTTP/1.1 415 Unsupported Media Type\nServer: idefav\nContent-Type: text/html;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
					//_, err := writer.WriteString(respContent)
					//if err != nil {
					//	log.Println(err)
					//}
					////log.Println(count)
					//writer.Flush()
					//c.Close()
					//conn.SetReadDeadline(time.Time{})
					if dst_host == "" {
						dst_host = "192.168.0.105:28081"
					}
					destConn, err := net.Dial("tcp", dst_host)
					if err != nil {
						conn.Close()
						return
					} else {
						go func() {
							_, err := reader.WriteTo(destConn)
							if err != nil {
								conn.Close()
								destConn.Close()
								return
							}
						}()
						_, err = io.Copy(conn, destConn)
						if err != nil {
							conn.Close()
							destConn.Close()
							return
						}
						log.Println("断开连接")
					}

				}
			}

		}()

	}
}

func (o *OutboundServer) Shutdown() error {
	for o.NumOpen > 0 {
		time.Sleep(time.Second)
		continue
	}
	return nil
}

var OutboundProxyServer = NewOutboundServer()
