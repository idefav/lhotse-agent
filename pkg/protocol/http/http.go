package http

import (
	"bufio"
	"errors"
	"net/textproto"
	"strings"
)

//type Http interface {
//	ReadRequest(reader *bufio.Reader) (Http, error)
//	ReadResponse(reader *bufio.Reader) (Http, error)
//}

type Headers map[string][]string

type Request struct {
	Version string
	Method  string
	Path    string
	Headers Headers
	Reader  *textproto.Reader
}

type CommonHttp struct {
	Request  Request
	Response Response
}

func (h Headers) GetHeader(name string) string {
	return textproto.MIMEHeader(h).Get(name)
}

func (h Headers) SetHeader(name string, value string) {
	textproto.MIMEHeader(h).Set(name, value)
}

func (h Headers) AddHeader(name, value string) {
	textproto.MIMEHeader(h).Add(name, value)
}

func (h Headers) Values(name string) []string {
	return textproto.MIMEHeader(h).Values(name)
}

func (h Headers) Del(name string) {
	textproto.MIMEHeader(h).Del(name)
}

func (r *Request) ReadRequest(reader *bufio.Reader) error {
	err := r.ReadRequestLine()
	if err != nil {
		return err
	}
	r.ReadHeader(reader)
	return nil
}

func (r *Request) ReadHeader(reader *bufio.Reader) {
	if r.Headers == nil {
		r.Headers = Headers{}
	}
	for {
		line, _, _ := reader.ReadLine()
		text := string(line)
		if text == "" {
			break
		}
		//split := strings.Split(text, HEADER_SPLIT)
		//r.Headers[strings.ToLower(split[0])] =
	}
}

func (r *Request) ReadRequestLine() error {
	line, err := r.Reader.ReadLine()
	if err != nil {
		return err
	}
	var requestLine = ""
	requestLine = line

	if requestLine == "" {
		return errors.New("request Line Format Error")
	}

	requestLineSplit := strings.Split(requestLine, REQUEST_LINE_SPLIT)
	var method = requestLineSplit[0]
	var path = requestLineSplit[1]
	var version = requestLineSplit[2]
	r.Method = method
	r.Version = version
	r.Path = path
	return nil
}

type Response struct {
	Headers Headers
}
