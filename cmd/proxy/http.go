package proxy

import (
	"bufio"
	"fmt"
	"lhotse-agent/cmd/proxy/data"
	lhotseHttp "lhotse-agent/pkg/protocol/http"
	"log"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

const (
	HEADER_SPLIT = ": "
	CRLF         = "\r\n"
)

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

func (inProxyServer *InProxyServer) HttpProc(conn net.Conn, reader *bufio.Reader, dst_host string) error {
	var requestLine = ""
	var headers = make(map[string]string)
	//log.Println("开始解析")
	reqLine, _, _ := reader.ReadLine()
	requestLine = string(reqLine)
	for {
		line, _, _ := reader.ReadLine()
		text := string(line)
		if text == "" {
			break
		}
		split := strings.Split(text, HEADER_SPLIT)
		headers[strings.ToLower(split[0])] = split[1]
	}

	connection := headers[strings.ToLower("Connection")]

	//log.Println(headers)
	//log.Println("链接目标服务")

	destConn, err0 := net.Dial("tcp", dst_host)
	//destConn := Get("192.168.0.105:28080")
	//destConn0, err0 := inProxyServer.ConnPool.Get()

	//defer destConn.Close()
	if err0 != nil {
		//log.Println("连接失败:")

		var body = ""
		body = "连接失败:" + err0.Error()
		var respContent = "HTTP/1.1 200 OK\nServer: idefav\nContent-Type: text/plain;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
		conn.Write([]byte(respContent))
		return nil
	}
	//destConn := *destConn0.C
	//log.Println("连接成功")

	var headerStr = requestLine + CRLF
	for k, v := range headers {
		headerStr += fmt.Sprintf("%s: %s", k, v) + CRLF
	}
	//log.Println("写入目标连接")
	contentLengthStr := headers[strings.ToLower("Content-Length")]
	//destConn.Write(bytes[:n])
	//n, _ := reader.Read(bytes)
	//var bytes = make([]byte, 1024)
	_, err2 := destConn.Write([]byte(headerStr + CRLF))
	contentLength, err3 := strconv.Atoi(contentLengthStr)
	if err3 != nil {
		contentLength = 0
	}
	if contentLength > 0 {
		var sumReadLen = 0
		for {
			var bytes = make([]byte, 1024)
			n, _ := reader.Read(bytes)
			sumReadLen += n
			destConn.Write(bytes[:n])
			if sumReadLen >= contentLength {
				break
			}
		}
	}
	//destConn.Write([]byte(CRLF))
	func() {
		//log.Println("开始响应")
		//respReader := destConn0.R
		respReader := bufio.NewReader(destConn)
		line, _, _ := respReader.ReadLine()
		conn.Write([]byte(string(line) + CRLF))
		conn.Write([]byte("Server: lhotse-agent" + CRLF))

		var chunked = false
		var responseConnValue = ""
		respContentLength := 0
		for {
			headerBytes, _, _ := respReader.ReadLine()
			header := string(headerBytes)
			//log.Println(header)
			if strings.Contains(header, "Transfer-Encoding") {
				split := strings.Split(header, HEADER_SPLIT)
				chunkedStr := split[1]
				chunked = strings.ToLower(chunkedStr) == "chunked"
				conn.Write([]byte(header + CRLF))
				continue
			}
			if header == "" {
				conn.Write([]byte(CRLF))
				break
			}
			if strings.HasPrefix(header, "Connection") {
				split := strings.Split(header, HEADER_SPLIT)
				v := split[1]
				responseConnValue = v
				conn.Write([]byte("Connection: keep-alive" + CRLF))
				continue
			}
			if strings.HasPrefix(header, "Keep-Alive") {
				conn.Write([]byte("Keep-Alive: timeout=60" + CRLF))
				continue
			}
			if strings.HasPrefix(header, "Content-Length") {
				split := strings.Split(header, HEADER_SPLIT)
				respContentLengthStr := split[1]
				respContentLen, err := strconv.Atoi(respContentLengthStr)
				if err != nil {
					respContentLength = 0
				} else {
					respContentLength = respContentLen
				}
			}
			conn.Write([]byte(header + CRLF))
		}

		for chunked {
			line, _, err := respReader.ReadLine()
			if err != nil {
				log.Println(err)
				return
			}

			lineText := string(line)
			conn.Write([]byte(lineText + CRLF))
			if lineText == "" {
				continue
			}

			chunkSize64, err := strconv.ParseInt(lineText, 16, 32)
			if err != nil {
				log.Println(err)
				return
			}
			chunkSize := int(chunkSize64)
			if chunkSize == 0 {
				conn.Write([]byte(CRLF))
				return
			}

			var sumReadLen = 0
			for {
				var tmpSize = 1024
				if chunkSize < 1024 {
					tmpSize = chunkSize
				}
				var bytes = make([]byte, tmpSize)
				l, _ := respReader.Read(bytes)
				sumReadLen += l
				conn.Write(bytes[:l])
				if sumReadLen >= chunkSize {
					//conn.Write([]byte(CRLF))
					break
				}
			}

		}

		if respContentLength > 0 {
			var sumReadLen = 0
			for {
				var bytes = make([]byte, 1024)
				l, _ := respReader.Read(bytes)
				sumReadLen += l
				conn.Write(bytes[:l])
				if sumReadLen >= respContentLength {
					break
				}
			}
		}
		conn.Write([]byte(CRLF))

		//c.Write([]byte("Connection: close\r\n"))
		//respReader.WriteTo(conn)
		//io.Copy(ctx.conn.c, destConn)
		//log.Println("响应结束")
		//destConn.Close()
		if strings.ToLower(responseConnValue) == "close" {
			//inProxyServer.ConnPool.Close(destConn0)
			conn.Close()
		} else {
			//log.Println("连接放回池子")
			//inProxyServer.ConnPool.Put(destConn0)
		}

		if strings.ToLower(connection) == "close" {
			conn.Close()
		}
	}()

	return err2

}

func (inProxyServer *InProxyServer) HttpProc2(conn net.Conn, reader *bufio.Reader, dst_host string) error {
	tp := textproto.NewReader(reader)
	request, err := lhotseHttp.ReadRequest(tp)
	if err != nil {
		return err
	}
	request.RemoteAddr = conn.RemoteAddr().String()
	request.LocalAddr = conn.LocalAddr().String()

	destConn, err0 := net.Dial("tcp", dst_host)
	if err0 != nil {
		var body = ""
		body = "连接失败:" + err0.Error()
		var respContent = "HTTP/1.1 200 OK\nServer: idefav\nContent-Type: text/plain;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
		conn.Write([]byte(respContent))
		return nil
	}

	var requestHeaderText = request.FormatRequestLine() + CRLF
	for k, hs := range request.Header {
		for _, h := range hs {
			var headerLine = fmt.Sprintf("%s: %s%s", k, h, CRLF)
			requestHeaderText += headerLine
		}
	}
	requestHeaderText += CRLF
	dstWriter := textproto.NewWriter(bufio.NewWriter(destConn))
	dstWriter.W.Write([]byte(requestHeaderText))
	dstWriter.W.Flush()

	if request.ContentLength > 0 {
		var sumReadLen int64 = 0
		for {
			var bytes = make([]byte, 1024)
			n, _ := reader.Read(bytes)
			sumReadLen += int64(n)
			destConn.Write(bytes[:n])
			if sumReadLen >= request.ContentLength {
				break
			}
		}
	}

	func() error {
		respReader := bufio.NewReader(destConn)
		respTpReader := textproto.NewReader(respReader)
		response, err2 := lhotseHttp.ReadResponse(respTpReader, request)
		if err2 != nil {
			return err
		}
		response.Header.Add("Server", inProxyServer.Cfg.ServerName)
		var responseHeaderText = response.FormatStatusLine() + CRLF
		for k, hs := range response.Header {
			for _, h := range hs {
				var headerLine = fmt.Sprintf("%s: %s%s", k, h, CRLF)
				responseHeaderText += headerLine
			}
		}
		responseHeaderText += CRLF
		conn.Write([]byte(responseHeaderText))

		for response.Chunked {
			line, _, err := respReader.ReadLine()
			if err != nil {
				log.Println(err)
				return err
			}

			lineText := string(line)
			conn.Write([]byte(lineText + CRLF))
			if lineText == "" {
				continue
			}

			chunkSize64, err := strconv.ParseInt(lineText, 16, 32)
			if err != nil {
				log.Println(err)
				return err
			}
			chunkSize := int(chunkSize64)
			if chunkSize == 0 {
				conn.Write([]byte(CRLF))
				return err
			}

			var sumReadLen = 0
			for {
				var tmpSize = 1024
				if chunkSize < 1024 {
					tmpSize = chunkSize
				}
				var bytes = make([]byte, tmpSize)
				l, _ := respReader.Read(bytes)
				sumReadLen += l
				conn.Write(bytes[:l])
				if sumReadLen >= chunkSize {
					//conn.Write([]byte(CRLF))
					break
				}
			}

		}

		if response.ContentLength > 0 {
			var sumReadLen int64 = 0
			for {
				var bytes = make([]byte, 1024)
				l, _ := respReader.Read(bytes)
				sumReadLen += int64(l)
				conn.Write(bytes[:l])
				if sumReadLen >= response.ContentLength {
					break
				}
			}
		}
		conn.Write([]byte(CRLF))

		if response.Close || request.Close {
			conn.Close()
		}
		return nil
	}()
	return nil

}

func (o *OutboundServer) HttpProc(conn net.Conn, reader *bufio.Reader, dst_host string) error {
	tp := textproto.NewReader(reader)
	request, err2 := lhotseHttp.ReadRequest(tp)
	if err2 != nil {
		return err2
	}

	endpoint, err := data.Match(request)
	if err == nil {
		dst_host = fmt.Sprintf("%s:%s", endpoint.Ip, endpoint.Port)
		log.Println(dst_host)
	}

	destConn, err0 := net.Dial("tcp", dst_host)
	if err0 != nil {
		var body = ""
		body = "连接失败:" + err0.Error()
		var respContent = "HTTP/1.1 200 OK\nServer: idefav\nContent-Type: text/plain;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
		conn.Write([]byte(respContent))
		return nil
	}

	var requestHeaderText = request.FormatRequestLine() + CRLF
	for k, hs := range request.Header {
		for _, h := range hs {
			var headerLine = fmt.Sprintf("%s: %s%s", k, h, CRLF)
			requestHeaderText += headerLine
		}
	}
	requestHeaderText += CRLF
	dstWriter := textproto.NewWriter(bufio.NewWriter(destConn))
	dstWriter.W.Write([]byte(requestHeaderText))
	dstWriter.W.Flush()

	if request.ContentLength > 0 {
		var sumReadLen int64 = 0
		for {
			var bytes = make([]byte, 1024)
			n, _ := tp.R.Read(bytes)
			sumReadLen += int64(n)
			dstWriter.W.Write(bytes[:n])
			dstWriter.W.Flush()
			if sumReadLen >= request.ContentLength {
				break
			}
		}
	}

	func() error {
		respReader := bufio.NewReader(destConn)
		respTpReader := textproto.NewReader(respReader)
		response, err3 := lhotseHttp.ReadResponse(respTpReader, request)
		if err3 != nil {
			return err3
		}
		response.Header.Add("Server", o.Cfg.ServerName)
		var responseHeaderText = response.FormatStatusLine() + CRLF
		for k, hs := range response.Header {
			for _, h := range hs {
				var headerLine = fmt.Sprintf("%s: %s%s", k, h, CRLF)
				responseHeaderText += headerLine
			}
		}
		responseHeaderText += CRLF
		conn.Write([]byte(responseHeaderText))

		for response.Chunked {
			line, _, err := respReader.ReadLine()
			if err != nil {
				log.Println(err)
				return err
			}

			lineText := string(line)
			conn.Write([]byte(lineText + CRLF))
			if lineText == "" {
				continue
			}

			chunkSize64, err := strconv.ParseInt(lineText, 16, 32)
			if err != nil {
				log.Println(err)
				return err
			}
			chunkSize := int(chunkSize64)
			if chunkSize == 0 {
				lastLine, _, _ := respReader.ReadLine()
				conn.Write([]byte(string(lastLine) + CRLF))
				return err
			}

			var sumReadLen = 0
			for {
				var tmpSize = 1024
				if chunkSize < 1024 {
					tmpSize = chunkSize
				}
				var bytes = make([]byte, tmpSize)
				l, _ := respReader.Read(bytes)
				sumReadLen += l
				conn.Write(bytes[:l])
				if sumReadLen >= chunkSize {
					break
				}
			}

		}

		if response.ContentLength > 0 {
			var sumReadLen int64 = 0
			for {
				var bytes = make([]byte, 1024)
				l, _ := respReader.Read(bytes)
				sumReadLen += int64(l)
				conn.Write(bytes[:l])
				if sumReadLen >= response.ContentLength {
					break
				}
			}

		}
		conn.Write([]byte(CRLF))
		if response.Close || request.Close {
			log.Println("链接关闭")
			conn.Close()
		}

		return nil
	}()

	return nil
}
