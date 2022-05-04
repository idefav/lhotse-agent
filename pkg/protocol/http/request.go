package http

import (
	"errors"
	"fmt"
	"golang.org/x/net/http/httpguts"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

//type Http interface {
//	ReadRequest(reader *bufio.Reader) (Http, error)
//	ReadResponse(reader *bufio.Reader) (Http, error)
//}

var (
	REQUEST_LINE_ERROR error = errors.New("request line format error")
)

type Request struct {
	Method           string
	Proto            string
	ProtoMajor       int
	ProtoMinor       int
	URL              *url.URL
	Port             int32
	Authority        string
	Header           Header
	ContentLength    int64
	TransferEncoding []string
	Host             string
	RemoteAddr       string
	LocalAddr        string
	RequestURI       string
	Close            bool
}

type Protocol struct {
	Raw     string
	Schema  string
	Version string
}

func badStringError(what, val string) error { return fmt.Errorf("%s %q", what, val) }
func unsupportedTEError(what, val interface{}) error {
	return errors.New(fmt.Sprintf("%s %q", what, val))
}
func ReadRequest(reader *textproto.Reader) (req *Request, err error) {
	req = new(Request)
	var requestLine string
	if requestLine, err = reader.ReadLine(); err != nil {
		return nil, err
	}
	var ok bool
	req.Method, req.RequestURI, req.Proto, ok = parseRequestLine(requestLine)
	if !ok {
		return nil, badStringError("malformed HTTP request", requestLine)
	}
	if !validMethod(req.Method) {
		return nil, badStringError("invalid method", req.Method)
	}
	rawurl := req.RequestURI
	if req.ProtoMajor, req.ProtoMinor, ok = ParseHTTPVersion(req.Proto); !ok {
		return nil, badStringError("malformed HTTP version", req.Proto)
	}
	justAuthority := req.Method == "CONNECT" && !strings.HasPrefix(rawurl, "/")
	if justAuthority {
		rawurl = "http://" + rawurl
	}
	if req.URL, err = url.ParseRequestURI(rawurl); err != nil {
		return nil, err
	}
	if justAuthority {
		// Strip the bogus "http://" back off.
		req.URL.Scheme = ""
	}
	// Subsequent lines: Key: value.
	mimeHeader, err := reader.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	req.Header = Header(mimeHeader)
	if len(req.Header["Host"]) > 1 {
		return nil, fmt.Errorf("too many Host headers")
	}

	req.Host = req.URL.Host
	if req.Host == "" {
		req.Host = req.Header.get("Host")
	}

	req.Close = shouldClose(req.ProtoMajor, req.ProtoMinor, req.Header, false)
	fixPragmaCacheControl(req.Header)
	fillContentLength(req)
	fillAuthority(req)
	fillTransferEncoding(req)
	fillPort(req)
	return req, nil

}

func (req *Request) FormatRequestLine() string {
	return fmt.Sprintf("%s %s %s", req.Method, req.RequestURI, req.Proto)
}

func fillPort(r *Request) (err error) {
	host := r.Host
	index := strings.Index(host, ":")
	if index < 0 {
		r.Port = 80
	}
	var port = host[index+1:]
	portI, err := strconv.Atoi(port)
	r.Port = int32(portI)
	return err
}

func shouldClose(major, minor int, header Header, removeCloseHeader bool) bool {
	if major < 1 {
		return true
	}

	conv := header["Connection"]
	hasClose := httpguts.HeaderValuesContainsToken(conv, "close")
	if major == 1 && minor == 0 {
		return hasClose || !httpguts.HeaderValuesContainsToken(conv, "keep-alive")
	}

	if hasClose && removeCloseHeader {
		header.Del("Connection")
	}

	return hasClose
}

func fillContentLength(r *Request) (err error) {
	contentLen := r.Header.get("Content-Length")
	cl, err := strconv.Atoi(contentLen)
	r.ContentLength = int64(cl)
	return err
}

func fillAuthority(r *Request) {
	authority := r.Header.get("Authorization")
	r.Authority = authority
}

func fillTransferEncoding(r *Request) {
	transferEncoding := r.Header.Values("Transfer-Encoding")
	r.TransferEncoding = transferEncoding
}

func parseRequestLine(line string) (method, requestURI, proto string, ok bool) {
	s1 := strings.Index(line, " ")
	s2 := strings.Index(line[s1+1:], " ")
	if s1 < 0 || s2 < 0 {
		return
	}
	s2 += s1 + 1
	return line[:s1], line[s1+1 : s2], line[s2+1:], true
}
func validMethod(method string) bool {
	/*
	     Method         = "OPTIONS"                ; Section 9.2
	                    | "GET"                    ; Section 9.3
	                    | "HEAD"                   ; Section 9.4
	                    | "POST"                   ; Section 9.5
	                    | "PUT"                    ; Section 9.6
	                    | "DELETE"                 ; Section 9.7
	                    | "TRACE"                  ; Section 9.8
	                    | "CONNECT"                ; Section 9.9
	                    | extension-method
	   extension-method = token
	     token          = 1*<any CHAR except CTLs or separators>
	*/
	return len(method) > 0 && strings.IndexFunc(method, isNotToken) == -1
}

func isNotToken(r rune) bool {
	return !httpguts.IsTokenRune(r)
}

// ParseHTTPVersion parses an HTTP version string.
// "HTTP/1.0" returns (1, 0, true). Note that strings without
// a minor version, such as "HTTP/2", are not valid.
func ParseHTTPVersion(vers string) (major, minor int, ok bool) {
	const Big = 1000000 // arbitrary upper bound
	switch vers {
	case "HTTP/1.1":
		return 1, 1, true
	case "HTTP/1.0":
		return 1, 0, true
	}
	if !strings.HasPrefix(vers, "HTTP/") {
		return 0, 0, false
	}
	dot := strings.Index(vers, ".")
	if dot < 0 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(vers[5:dot])
	if err != nil || major < 0 || major > Big {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(vers[dot+1:])
	if err != nil || minor < 0 || minor > Big {
		return 0, 0, false
	}
	return major, minor, true
}

func fixPragmaCacheControl(header Header) {
	if hp, ok := header["Pragma"]; ok && len(hp) > 0 && hp[0] == "no-cache" {
		if _, presentcc := header["Cache-Control"]; !presentcc {
			header["Cache-Control"] = []string{"no-cache"}
		}
	}
}
