package proxy

import (
	"bufio"
	"fmt"
	"io"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/socket"
	"log"
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

	go inProxyServer.proc(ln)

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

		go func() {
			defer conn.Close()
			atomic.AddInt32(&inProxyServer.NumOpen, 1)
			defer atomic.AddInt32(&inProxyServer.NumOpen, -1)
			log.Println("numOpen:", inProxyServer.NumOpen)
			log.Printf("removeAddr: %s --> localAddr: %s", conn.RemoteAddr(), conn.LocalAddr())

			var dst_host = ""
			_, host, _, err := socket.GetOriginalDst(conn.(*net.TCPConn))
			//log.Println(dst, host, tcpConn, err)
			if err == nil {
				dst_host = host
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
					//log.Println("开始Http协议解析")
					if dst_host == "" {
						dst_host = "192.168.0.105:28080"
					}
					inProxyServer.HttpProc2(conn, reader, dst_host)
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
