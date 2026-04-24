package mitm

import (
	"bytes"
	"io"
	"net"
)

// BufferedConn wraps a net.Conn with some pre-read buffered data.
type BufferedConn struct {
	net.Conn
	reader io.Reader
}

func NewBufferedConn(c net.Conn, preRead []byte) *BufferedConn {
	return &BufferedConn{
		Conn:   c,
		reader: io.MultiReader(bytes.NewReader(preRead), c),
	}
}

func (b *BufferedConn) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}
