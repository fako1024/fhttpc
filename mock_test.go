package fhttpc

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

type MockMatchFn func(ctx *fasthttp.RequestCtx) (bool, error)

type Mock struct {
	method string
	uri    string

	ln      net.Listener
	client  *fasthttp.Client
	handler fasthttp.RequestHandler

	reply       int
	respHeaders Params
}

type MultiMock struct {
	mocks  map[string]*Mock
	client *fasthttp.Client
}

func NewMultiMock(mocks ...*Mock) (*MultiMock, error) {

	inMemListener := fasthttputil.NewInmemoryListener()
	obj := &MultiMock{
		mocks: make(map[string]*Mock),
		client: &fasthttp.Client{
			Dial: func(addr string) (net.Conn, error) {
				return inMemListener.Dial()
			},
		},
	}
	for _, mock := range mocks {
		mock.Close()
		uri, err := url.Parse(mock.uri)
		if err != nil {
			return nil, fmt.Errorf("failed to parse mock URI `%s` for MultiMock routing: %w", mock.uri, err)
		}
		obj.mocks[strings.ToLower(mock.method+"_"+uri.Path)] = mock
	}

	go func() {
		if err := fasthttp.Serve(inMemListener, func(ctx *fasthttp.RequestCtx) {
			mock, ok := obj.mocks[strings.ToLower(string(ctx.Method())+"_"+string(ctx.Path()))]
			if !ok {
				ctx.Error("MOCK: no matching mock handler found", fasthttp.StatusNotFound)
				return
			}

			mock.handler(ctx)
		}); err != nil {
			panic(err)
		}
	}()

	return obj, nil
}

func (m *MultiMock) Client() *fasthttp.Client {
	return m.client
}

func (m *MultiMock) Close() (err error) {
	for _, mock := range m.mocks {
		err = mock.Close()
	}
	return err
}

func NewMock(method, uri string, t testCase, matchFns ...MockMatchFn) *Mock {

	inMemListener := fasthttputil.NewInmemoryListener()
	m := &Mock{
		method: method,
		uri:    uri,

		ln:    inMemListener,
		reply: t.expectedStatusCode,
	}
	m.client = &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) {
			return inMemListener.Dial()
		},
	}

	if strings.HasPrefix(uri, "https://") {
		cert, err := tls.X509KeyPair([]byte(testServerCert), []byte(testServerKey))
		if err != nil {
			panic(err)
		}

		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			panic(err)
		}
		if !caCertPool.AppendCertsFromPEM([]byte(testCACert)) {
			panic(err)
		}

		m.ln = tls.NewListener(m.ln, &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    caCertPool,
			MinVersion:   tls.VersionTLS12,
		})

		m.client.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	m.handler = func(ctx *fasthttp.RequestCtx) {

		// Handle hostnames
		if t.hostName != "" {
			if string(ctx.Request.Header.Peek("Host")) != t.hostName {
				ctx.Error(fmt.Sprintf("MOCK: non-matching host name (want %s, have %s)", t.hostName, ctx.Request.Header.Peek("Host")), fasthttp.StatusInternalServerError)
				return
			}
		}

		// Check for method
		if string(ctx.Method()) != m.method {
			ctx.Error(fmt.Sprintf("MOCK: non-matching method (want %s, have %s)", m.method, ctx.Method()), fasthttp.StatusInternalServerError)
			return
		}

		uri, err := url.Parse(m.uri)
		if err != nil {
			ctx.Error(fmt.Sprintf("MOCK: invalid expected URI (%s): %s", uri, err), fasthttp.StatusInternalServerError)
			return
		}

		// Check for URI path
		if string(ctx.Path()) != uri.Path {
			ctx.Error(fmt.Sprintf("MOCK: non-matching URI path (want %s, have %s)", uri.Path, ctx.RequestURI()), fasthttp.StatusInternalServerError)
			return
		}

		// Handle query parameters
		if len(t.queryParams) > 0 {
			q := ctx.URI().QueryArgs()
			if q.Len() != len(t.queryParams) {
				ctx.Error(fmt.Sprintf("MOCK: non-matching query args (want %v, have %v)", t.queryParams, ctx.URI().QueryArgs()), fasthttp.StatusInternalServerError)
				return
			}
			for key, val := range t.queryParams {
				if string(q.Peek(key)) != val {
					ctx.Error(fmt.Sprintf("MOCK: non-matching query args (want %v, have %v)", t.queryParams, ctx.URI().QueryArgs()), fasthttp.StatusInternalServerError)
					return
				}
			}
		}

		// Handle headers
		if len(t.headers) > 0 {
			for key, val := range t.headers {
				if string(ctx.Request.Header.Peek(key)) != val {
					ctx.Error(fmt.Sprintf("MOCK: non-matching header (want %v, have %v)", t.headers, string(ctx.Request.Header.Peek(key))), fasthttp.StatusInternalServerError)
					return
				}
			}
		}

		// Handle request body
		if t.requestBody != nil {
			if !bytes.Equal(ctx.Request.Body(), t.requestBody) {
				ctx.Error(fmt.Sprintf("MOCK: non-matching body (want len %d, have %d)", len(t.requestBody), len(ctx.Request.Body())), fasthttp.StatusInternalServerError)
				return
			}
		}

		for _, matchFn := range matchFns {
			matches, err := matchFn(ctx)
			if err != nil {
				ctx.Error(fmt.Sprintf("MOCK: error executing match function: %s", err), fasthttp.StatusInternalServerError)
				return
			}
			if !matches {
				ctx.Error("MOCK: non-matching function call", fasthttp.StatusNotFound)
				return
			}
		}

		// Define the return code (and body, if provided)
		if t.responseBody != nil {
			ctx.Response.SetBody(t.responseBody)
		}

		// Set mock response headers
		for k, v := range m.respHeaders {
			ctx.Response.Header.Set(k, v)
		}

		// Set mock response status code (if requested, otherwise use the expected status code from the test)
		if m.reply != 0 {
			ctx.Response.SetStatusCode(m.reply)
		}

		// Delay response if requested
		time.Sleep(t.respDelay)
	}

	go m.run()

	return m
}

func (m *Mock) run() {
	if err := fasthttp.Serve(m.ln, m.handler); err != nil {
		panic(err)
	}
}

func (m *Mock) RespHeaders(headers Params) *Mock {
	m.respHeaders = headers
	return m
}

func (m *Mock) Reply(reply int) *Mock {
	m.reply = reply
	return m
}

func (m *Mock) Client() *fasthttp.Client {
	return m.client
}

func (m *Mock) Close() error {
	return m.ln.Close()
}
