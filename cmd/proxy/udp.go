package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/pkg/log"
	"lhotse-agent/pkg/socket"
)

type udpSession struct {
	key      string
	client   *net.UDPAddr
	original *net.UDPAddr
	upstream *net.UDPConn
}

type UDPServer struct {
	cfg         *config.Config
	idleTimeout time.Duration
	port        int32

	conn *net.UDPConn

	mu        sync.Mutex
	sessions  map[string]*udpSession
	replyConn map[string]*net.UDPConn
}

func NewUDPServer(idleTimeout time.Duration, port int32, cfg *config.Config) *UDPServer {
	return &UDPServer{
		cfg:         cfg,
		idleTimeout: idleTimeout,
		port:        port,
		sessions:    map[string]*udpSession{},
		replyConn:   map[string]*net.UDPConn{},
	}
}

func (u *UDPServer) Startup() error {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			if err := c.Control(func(fd uintptr) {
				controlErr = socket.EnableUDPSocketOptions(int(fd))
			}); err != nil {
				return err
			}
			return controlErr
		},
	}

	packetConn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", u.port))
	if err != nil {
		return err
	}

	udpConn, ok := packetConn.(*net.UDPConn)
	if !ok {
		_ = packetConn.Close()
		return fmt.Errorf("expected UDPConn, got %T", packetConn)
	}

	u.conn = udpConn
	go u.serve()
	return nil
}

func (u *UDPServer) Shutdown() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.conn != nil {
		_ = u.conn.Close()
	}
	for _, session := range u.sessions {
		_ = session.upstream.Close()
	}
	for _, conn := range u.replyConn {
		_ = conn.Close()
	}
	return nil
}

func (u *UDPServer) serve() {
	buf := make([]byte, 64*1024)
	oob := make([]byte, 512)

	for {
		n, oobn, _, clientAddr, err := u.conn.ReadMsgUDP(buf, oob)
		if err != nil {
			return
		}

		originalDst, err := socket.ParseOriginalDstFromOOB(oob[:oobn])
		if err != nil {
			log.Errorf("udp original dst lookup failed: %v", err)
			continue
		}

		session, err := u.getOrCreateSession(clientAddr, originalDst)
		if err != nil {
			log.Errorf("udp session setup failed for %s -> %s: %v", clientAddr, originalDst, err)
			continue
		}

		_ = session.upstream.SetWriteDeadline(time.Now().Add(u.idleTimeout))
		if _, err := session.upstream.Write(buf[:n]); err != nil {
			log.Errorf("udp upstream write failed for %s -> %s: %v", clientAddr, originalDst, err)
			u.closeSession(session.key)
		}
	}
}

func (u *UDPServer) getOrCreateSession(clientAddr, originalDst *net.UDPAddr) (*udpSession, error) {
	key := fmt.Sprintf("%s|%s", clientAddr.String(), originalDst.String())

	u.mu.Lock()
	defer u.mu.Unlock()

	if session, ok := u.sessions[key]; ok {
		return session, nil
	}

	upstreamConn, err := net.DialUDP("udp4", nil, originalDst)
	if err != nil {
		return nil, err
	}

	session := &udpSession{
		key:      key,
		client:   cloneUDPAddr(clientAddr),
		original: cloneUDPAddr(originalDst),
		upstream: upstreamConn,
	}
	u.sessions[key] = session

	go u.proxyReplies(session)
	return session, nil
}

func (u *UDPServer) proxyReplies(session *udpSession) {
	buf := make([]byte, 64*1024)

	for {
		_ = session.upstream.SetReadDeadline(time.Now().Add(u.idleTimeout))
		n, err := session.upstream.Read(buf)
		if err != nil {
			u.closeSession(session.key)
			return
		}

		replyConn, err := u.getReplyConn(session.original)
		if err != nil {
			log.Errorf("udp reply socket setup failed for %s: %v", session.original, err)
			u.closeSession(session.key)
			return
		}

		_ = replyConn.SetWriteDeadline(time.Now().Add(u.idleTimeout))
		if _, err := replyConn.WriteToUDP(buf[:n], session.client); err != nil {
			log.Errorf("udp client write failed for %s -> %s: %v", session.original, session.client, err)
			u.closeSession(session.key)
			return
		}
	}
}

func (u *UDPServer) getReplyConn(originalDst *net.UDPAddr) (*net.UDPConn, error) {
	key := originalDst.String()

	u.mu.Lock()
	defer u.mu.Unlock()

	if conn, ok := u.replyConn[key]; ok {
		return conn, nil
	}

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			if err := c.Control(func(fd uintptr) {
				controlErr = socket.EnableTransparentSocketOptions(int(fd))
			}); err != nil {
				return err
			}
			return controlErr
		},
	}

	packetConn, err := lc.ListenPacket(context.Background(), "udp4", originalDst.String())
	if err != nil {
		return nil, err
	}

	udpConn, ok := packetConn.(*net.UDPConn)
	if !ok {
		_ = packetConn.Close()
		return nil, fmt.Errorf("expected UDPConn, got %T", packetConn)
	}

	u.replyConn[key] = udpConn
	return udpConn, nil
}

func (u *UDPServer) closeSession(key string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	session, ok := u.sessions[key]
	if !ok {
		return
	}
	_ = session.upstream.Close()
	delete(u.sessions, key)
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	ip := make(net.IP, len(addr.IP))
	copy(ip, addr.IP)
	return &net.UDPAddr{IP: ip, Port: addr.Port, Zone: addr.Zone}
}
