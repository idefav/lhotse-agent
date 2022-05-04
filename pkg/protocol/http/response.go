package http

import (
	"fmt"
	"io"
	"lhotse-agent/pkg/protocol/http/internal/ascii"
	"net/textproto"
	"strconv"
	"strings"
)

type Response struct {
	Status           string // e.g. "200 OK"
	StatusCode       int    // e.g. 200
	Proto            string // e.g. "HTTP/1.0"
	ProtoMajor       int    // e.g. 1
	ProtoMinor       int    // e.g. 0
	Header           Header
	ContentLength    int64
	TransferEncoding []string
	Trailer          Header
	Request          *Request
	Chunked          bool
	Close            bool
}

func ReadResponse(tp *textproto.Reader, req *Request) (resp *Response, err error) {
	resp = &Response{
		Request: req,
	}

	// Parse the first line of the response.
	line, err := tp.ReadLine()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if i := strings.IndexByte(line, ' '); i == -1 {
		return nil, badStringError("malformed HTTP response", line)
	} else {
		resp.Proto = line[:i]
		resp.Status = strings.TrimLeft(line[i+1:], " ")
	}
	statusCode := resp.Status
	if i := strings.IndexByte(resp.Status, ' '); i != -1 {
		statusCode = resp.Status[:i]
	}
	if len(statusCode) != 3 {
		return nil, badStringError("malformed HTTP status code", statusCode)
	}
	resp.StatusCode, err = strconv.Atoi(statusCode)
	if err != nil || resp.StatusCode < 0 {
		return nil, badStringError("malformed HTTP status code", statusCode)
	}
	var ok bool
	if resp.ProtoMajor, resp.ProtoMinor, ok = ParseHTTPVersion(resp.Proto); !ok {
		return nil, badStringError("malformed HTTP version", resp.Proto)
	}

	mimeHeader, err := tp.ReadMIMEHeader()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	resp.Header = Header(mimeHeader)

	fixPragmaCacheControl(resp.Header)

	fillRespContentLength(resp)
	fillRespTransferEncoding(resp)
	shouldClose(resp.ProtoMajor, resp.ProtoMinor, resp.Header, false)
	resp.parseTransferEncoding()
	return resp, nil
}

func fillRespContentLength(resp *Response) {
	contentLengthS := resp.Header.get("Content-Length")
	contentLen, err := strconv.Atoi(contentLengthS)
	if err != nil {
		resp.ContentLength = 0
	} else {
		resp.ContentLength = int64(contentLen)
	}
}

func fillRespTransferEncoding(resp *Response) {
	resp.TransferEncoding = resp.Header.Values("Transfer-Encoding")
}

func (resp *Response) FormatStatusLine() string {
	return fmt.Sprintf("%s %s", resp.Proto, resp.Status)
}

func (resp *Response) protoAtLeast(m, n int) bool {
	return resp.ProtoMajor > m || (resp.ProtoMajor == m && resp.ProtoMinor >= n)
}

func (resp *Response) parseTransferEncoding() error {
	raw, present := resp.Header["Transfer-Encoding"]
	if !present {
		return nil
	}
	if !resp.protoAtLeast(1, 1) {
		return nil
	}
	if len(raw) != 1 {
		return unsupportedTEError("too many transfer encodings: %q", raw)
	}
	if !ascii.EqualFold(textproto.TrimString(raw[0]), "chunked") {
		return unsupportedTEError("unsupported transfer encoding: %q", raw[0])
	}
	resp.Chunked = true
	return nil
}
