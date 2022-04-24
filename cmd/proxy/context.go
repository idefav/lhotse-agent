package proxy

import (
	"bufio"
	"net"
)

type Context struct {
	conn   Conn
	ln     net.Listener
	server InProxyServer
}

type Conn struct {
	c net.Conn
	r *bufio.Reader
	w *bufio.Writer
}

func NewConn(conn net.Conn) Conn {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	return Conn{
		c: conn,
		r: reader,
		w: writer,
	}
}

func CreateContext() Context {
	return Context{}
}
