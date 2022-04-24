package proxy

import (
	"bufio"
	"fmt"
	"log"
	"net"
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

func (o *OutboundServer) HttpProc(conn net.Conn, reader *bufio.Reader, dst_host string) error {
	var requestLine = ""
	var headers = make(map[string]string)
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
	destConn, err0 := net.Dial("tcp", dst_host)
	if err0 != nil {
		var body = ""
		body = "连接失败:" + err0.Error()
		var respContent = "HTTP/1.1 200 OK\nServer: idefav\nContent-Type: text/plain;charset=UTF-8\nContent-Length: " + strconv.Itoa(len(body)) + "\n\n" + body + "\n"
		conn.Write([]byte(respContent))
		return nil
	}

	var headerStr = requestLine + CRLF
	for k, v := range headers {
		headerStr += fmt.Sprintf("%s: %s", k, v) + CRLF
	}
	contentLengthStr := headers[strings.ToLower("Content-Length")]
	_, err2 := destConn.Write([]byte(headerStr + CRLF))
	log.Println(headerStr + CRLF)
	if err2 != nil {
		log.Println(err2)
	}
	contentLength, err3 := strconv.Atoi(contentLengthStr)
	if err3 != nil {
		contentLength = 0
		log.Println(err3)
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
	func() {
		respReader := bufio.NewReader(destConn)
		line, _, _ := respReader.ReadLine()
		conn.Write([]byte(string(line) + CRLF))
		conn.Write([]byte("Server: lhotse-agent" + CRLF))

		var responseConnValue = ""
		var chunked = false
		respContentLength := 0
		for {
			headerBytes, _, _ := respReader.ReadLine()
			header := string(headerBytes)
			log.Println(header)
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
				//conn.Write([]byte("Connection: keep-alive" + CRLF))
				continue
			}
			if strings.HasPrefix(header, "Keep-Alive") {
				//conn.Write([]byte("Keep-Alive: timeout=60" + CRLF))
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
				lastLine, _, _ := respReader.ReadLine()
				conn.Write([]byte(string(lastLine) + CRLF))
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
		if strings.ToLower(responseConnValue) == "close" {
			log.Println("链接关闭")
			conn.Close()
		}

		if strings.ToLower(connection) == "close" {
			log.Println("链接关闭")
			conn.Close()
		}
	}()

	return err2
}
