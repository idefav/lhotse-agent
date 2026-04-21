package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"lhotse-agent/cmd/proxy/data"
	lhotseHttp "lhotse-agent/pkg/protocol/http"
	"lhotse-agent/util"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HEADER_SPLIT = ": "
	CRLF         = "\r\n"
)

var errProxyConnectionDone = errors.New("proxy connection done")

var destConnPool = ConnPool{
	conns:         make(map[string]net.Conn),
	m:             sync.RWMutex{},
	idleCount:     0,
	maxConnsCount: 100,
}

type ConnPool struct {
	conns         map[string]net.Conn
	idleCount     int32
	maxConnsCount int32
	m             sync.RWMutex
}

type closeWriter interface {
	CloseWrite() error
}

type closeReader interface {
	CloseRead() error
}

func Get(addr string) net.Conn {
	var key = addr
	result := func() net.Conn {
		destConnPool.m.RLock()
		defer destConnPool.m.RUnlock()
		conn := destConnPool.conns[key]
		if conn != nil {
			return conn
		}
		return nil
	}()
	if result == nil {
		result = func() net.Conn {
			destConnPool.m.Lock()
			defer destConnPool.m.Unlock()
			destConn, _ := net.Dial("tcp", addr)
			destConnPool.conns[key] = destConn
			return destConn
		}()
	}
	return destConnPool.conns[key]
}

func (inProxyServer *InProxyServer) HttpProc(conn net.Conn, reader *bufio.Reader, dstHost string) error {
	return inProxyServer.HttpProc2(conn, reader, dstHost, dstHost)
}

func (inProxyServer *InProxyServer) HttpProc2(conn net.Conn, reader *bufio.Reader, dstHost string, dialTarget string) error {
	tp := textproto.NewReader(reader)
	request, err := lhotseHttp.ReadRequest(tp)
	if err != nil {
		return err
	}
	request.RemoteAddr = conn.RemoteAddr().String()
	request.LocalAddr = conn.LocalAddr().String()
	logHTTPRequest("inbound", conn, dstHost, request)
	return proxyHTTPRequest("inbound", conn, reader, request, dstHost, dialTarget, inProxyServer.Cfg.ServerName)
}

func (o *OutboundServer) HttpProc(conn net.Conn, reader *bufio.Reader, dstHost string) error {
	tp := textproto.NewReader(reader)
	request, err := lhotseHttp.ReadRequest(tp)
	if err != nil {
		return err
	}

	endpoint, matchErr := data.Match(request)
	if matchErr == nil {
		dstHost = fmt.Sprintf("%s:%s", endpoint.Ip, endpoint.Port)
	}
	logHTTPRequest("outbound", conn, dstHost, request)
	return proxyHTTPRequest("outbound", conn, reader, request, dstHost, dstHost, o.Cfg.ServerName)
}

func proxyHTTPRequest(direction string, downstream net.Conn, reader *bufio.Reader, request *lhotseHttp.Request, targetAddr string, dialTarget string, serverName string) error {
	clearReadDeadline(downstream)

	upstream, err := net.Dial("tcp", dialTarget)
	if err != nil {
		body := "连接失败:" + err.Error()
		respContent := "HTTP/1.1 200 OK\nServer: idefav\nContent-Type: text/plain;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
		_, _ = downstream.Write([]byte(respContent))
		return errProxyConnectionDone
	}

	if request.Method == "CONNECT" {
		defer upstream.Close()
		if err := writeConnectEstablished(downstream, serverName); err != nil {
			return err
		}
		logUpgradedConnection(direction, downstream, targetAddr, request, protoCONNECT)
		if err := proxyTunnel(downstream, upstream, reader); err != nil {
			return err
		}
		return errProxyConnectionDone
	}

	defer upstream.Close()

	if err := writeHTTPRequest(upstream, reader, request); err != nil {
		return err
	}

	respReader := bufio.NewReader(upstream)
	respTpReader := textproto.NewReader(respReader)
	response, err := lhotseHttp.ReadResponse(respTpReader, request)
	if err != nil {
		return err
	}

	if response.Upgraded {
		logUpgradedConnection(direction, downstream, targetAddr, request, response.UpgradeType)
		if err := writeHTTPResponse(downstream, response, ""); err != nil {
			return err
		}
		if err := proxyTunnel(downstream, upstream, reader); err != nil {
			return err
		}
		return errProxyConnectionDone
	}

	if err := writeHTTPResponse(downstream, response, serverName); err != nil {
		return err
	}
	if err := proxyHTTPBody(downstream, respReader, request, response); err != nil {
		return err
	}

	if request.Close || response.Close {
		return errProxyConnectionDone
	}
	return nil
}

func writeHTTPRequest(upstream net.Conn, reader *bufio.Reader, request *lhotseHttp.Request) error {
	requestHeaderText := request.FormatRequestLine() + CRLF
	for k, hs := range request.Header {
		for _, h := range hs {
			requestHeaderText += fmt.Sprintf("%s: %s%s", k, h, CRLF)
		}
	}
	requestHeaderText += CRLF

	bufferedWriter := bufio.NewWriter(upstream)
	dstWriter := textproto.NewWriter(bufferedWriter)
	if _, err := dstWriter.W.Write([]byte(requestHeaderText)); err != nil {
		return err
	}
	if err := dstWriter.W.Flush(); err != nil {
		return err
	}

	switch {
	case requestIsChunked(request):
		if err := proxyChunkedBody(dstWriter.W, reader); err != nil {
			return err
		}
		return dstWriter.W.Flush()
	case request.ContentLength > 0:
		if err := copyFixedLength(dstWriter.W, reader, request.ContentLength); err != nil {
			return err
		}
		return dstWriter.W.Flush()
	default:
		return nil
	}
}

func writeHTTPResponse(downstream net.Conn, response *lhotseHttp.Response, serverName string) error {
	headers := response.Header.Clone()
	if serverName != "" {
		headers.Add("Server", serverName)
	}

	responseHeaderText := response.FormatStatusLine() + CRLF
	for k, hs := range headers {
		for _, h := range hs {
			responseHeaderText += fmt.Sprintf("%s: %s%s", k, h, CRLF)
		}
	}
	responseHeaderText += CRLF
	_, err := downstream.Write([]byte(responseHeaderText))
	return err
}

func writeConnectEstablished(downstream net.Conn, serverName string) error {
	response := "HTTP/1.1 200 Connection Established\r\n"
	if serverName != "" {
		response += fmt.Sprintf("Server: %s\r\n", serverName)
	}
	response += "\r\n"
	_, err := downstream.Write([]byte(response))
	return err
}

func proxyHTTPBody(downstream io.Writer, respReader *bufio.Reader, request *lhotseHttp.Request, response *lhotseHttp.Response) error {
	if responseHasNoBody(request, response) {
		return nil
	}

	switch {
	case response.Chunked:
		return proxyChunkedBody(downstream, respReader)
	case response.ContentLength >= 0:
		return copyFixedLength(downstream, respReader, response.ContentLength)
	case response.Close:
		_, err := io.Copy(downstream, respReader)
		if err == io.EOF {
			return nil
		}
		return err
	default:
		return nil
	}
}

func proxyChunkedBody(dst io.Writer, src *bufio.Reader) error {
	for {
		line, err := readChunkLine(src)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(dst, line); err != nil {
			return err
		}

		chunkSize, err := parseChunkSize(line)
		if err != nil {
			return err
		}
		if chunkSize == 0 {
			for {
				trailerLine, err := readChunkLine(src)
				if err != nil {
					return err
				}
				if _, err := io.WriteString(dst, trailerLine); err != nil {
					return err
				}
				if trailerLine == CRLF {
					return nil
				}
			}
		}

		if err := copyFixedLength(dst, src, int64(chunkSize)); err != nil {
			return err
		}
		if _, err := io.CopyN(dst, src, int64(len(CRLF))); err != nil {
			return err
		}
	}
}

func copyFixedLength(dst io.Writer, src io.Reader, remaining int64) error {
	if remaining == 0 {
		return nil
	}
	_, err := io.CopyN(dst, src, remaining)
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func readChunkLine(src *bufio.Reader) (string, error) {
	line, err := src.ReadString('\n')
	if err != nil {
		return "", err
	}
	return line, nil
}

func parseChunkSize(line string) (int64, error) {
	sizeText := strings.TrimSpace(line)
	if idx := strings.Index(sizeText, ";"); idx >= 0 {
		sizeText = sizeText[:idx]
	}
	return strconv.ParseInt(sizeText, 16, 64)
}

func responseHasNoBody(request *lhotseHttp.Request, response *lhotseHttp.Response) bool {
	if request.Method == "HEAD" {
		return true
	}
	if response.StatusCode >= 100 && response.StatusCode < 200 {
		return true
	}
	switch response.StatusCode {
	case 204, 304:
		return true
	}
	return false
}

func requestIsChunked(request *lhotseHttp.Request) bool {
	for _, value := range request.TransferEncoding {
		if strings.EqualFold(strings.TrimSpace(value), "chunked") {
			return true
		}
	}
	for _, value := range strings.Split(request.Header.Get("Transfer-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "chunked") {
			return true
		}
	}
	return false
}

func proxyRawConnection(downstream net.Conn, upstream net.Conn, reader *bufio.Reader) error {
	clearReadDeadline(downstream)
	if err := proxyTunnel(downstream, upstream, reader); err != nil {
		return err
	}
	return errProxyConnectionDone
}

func proxyTunnel(downstream net.Conn, upstream net.Conn, downstreamReader *bufio.Reader) error {
	errC := make(chan error, 2)
	copyPipe := func(dst net.Conn, src io.Reader) {
		_, err := io.Copy(dst, src)
		closeTunnelWrite(dst)
		closeTunnelRead(src)
		if err == nil || errors.Is(err, io.EOF) {
			errC <- nil
			return
		}
		errC <- err
	}

	util.GO(func() {
		copyPipe(upstream, downstreamReader)
	})
	util.GO(func() {
		copyPipe(downstream, upstream)
	})

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errC; err != nil && firstErr == nil {
			firstErr = err
		}
	}

	_ = upstream.Close()
	if firstErr != nil {
		_ = downstream.Close()
	}
	return firstErr
}

func closeTunnelWrite(conn net.Conn) {
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

func closeTunnelRead(src io.Reader) {
	if cr, ok := src.(closeReader); ok {
		_ = cr.CloseRead()
	}
}

func clearReadDeadline(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Time{})
}
