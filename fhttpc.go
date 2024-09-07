package fhttpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
)

var defaultacceptedResponseCodes = []int{
	fasthttp.StatusOK,
	fasthttp.StatusCreated,
	fasthttp.StatusAccepted,
	fasthttp.StatusPermanentRedirect,
	fasthttp.StatusTemporaryRedirect,
}

var defaultRetryErrFn = func(req *Request, resp *fasthttp.Response, err error) bool {
	return err != nil ||
		!isAnyOf(resp.StatusCode(), req.acceptedResponseCodes)
}

// Params is an alias for a map of string key / value pairs
type Params = map[string]string

// Intervals is an alias for a list of durations / time intervals
type Intervals = []time.Duration

// RetryErrFn denotes a function that returns true if a response is deemed a failured
// and has to be retried
type RetryErrFn func(req *Request, resp *fasthttp.Response, err error) bool

// RetryEventFn denotes a function that is executed if a retry has been triggered
type RetryEventFn func(attempt int, resp *fasthttp.Response, err error)

// Request represents a generic web request for quick execution, providing access
// to method, URL parameters, headers, the body and an optional 1st class function
// used to parse the result
type Request struct {
	method      string
	uri         string
	host        string
	timeout     time.Duration
	queryParams Params
	headers     Params
	parseFn     func(resp *fasthttp.Response) error
	errorFn     func(resp *fasthttp.Response) error

	jar *Jar

	bodyEncoder Encoder
	body        []byte

	delay          time.Duration
	retryIntervals Intervals
	retryErrFn     RetryErrFn
	retryEventFn   RetryEventFn

	acceptedResponseCodes []int
	client                *fasthttp.Client
	httpClientFunc        func(c *fasthttp.Client)
	httpRequestFunc       func(c *fasthttp.Request) error
	httpAuthFunc          func(c *fasthttp.Request)
}

// New instantiates a new http client
func New(method, uri string) *Request {
	// Instantiate a new NectIdent service using default options
	return NewWithClient(method, uri, nil)
}

// NewWithClient instantiates a new httpc request with custom http client
// This eases testing and client interception
func NewWithClient(method, uri string, hc *fasthttp.Client) *Request {
	r := &Request{
		method:                method,
		uri:                   uri,
		acceptedResponseCodes: defaultacceptedResponseCodes,
		client:                hc,
	}

	if r.client == nil {
		r.client = &fasthttp.Client{
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}

	return r
}

// GetMethod returns the method of the request
func (r *Request) GetMethod() string {
	return r.method
}

// GetURI returns the URI of the request
func (r *Request) GetURI() string {
	return r.uri
}

// GetBody returns the body of the request
func (r *Request) GetBody() []byte {
	return r.body
}

// HostName sets an explicit hostname for the client call
func (r *Request) HostName(host string) *Request {
	r.host = host
	return r
}

// Timeout sets timeout for the client call
func (r *Request) Timeout(timeout time.Duration) *Request {
	r.timeout = timeout
	return r
}

// RetryBackOff sets back-off intervals and attempts the call multiple times
func (r *Request) RetryBackOff(intervals Intervals) *Request {
	r.retryIntervals = intervals
	return r
}

// RetryBackOffErrFn sets an assessment function to decide wether an error
// or status code is deemed as a reason to retry the call.
// Note: both the response and the error may be nil (depending on the reason for the retry)
// so proper checks must be ensured
func (r *Request) RetryBackOffErrFn(fn RetryErrFn) *Request {
	r.retryErrFn = fn
	return r
}

// RetryEventFn sets an event handler / function that gets triggered upon a retry, providing
// the HTTP response and error that causes the retry.
// Note: both the response and the error may be nil (depending on the reason for the retry)
// so proper checks must be ensured
func (r *Request) RetryEventFn(fn RetryEventFn) *Request {
	r.retryEventFn = fn
	return r
}

// SkipCertificateVerification will accept any SSL certificate
func (r *Request) SkipCertificateVerification() *Request {
	r.client.TLSConfig.InsecureSkipVerify = true

	return r
}

// ClientCertificates sets client certificates from memory
func (r *Request) ClientCertificates(clientCert, clientKey, caCert []byte) (*Request, error) {
	tlsConfig, err := setupClientCertificateFromBytes(clientCert, clientKey, caCert, r.client.TLSConfig)
	if err != nil {
		return r, err
	}

	r.client.TLSConfig = tlsConfig

	return r, nil
}

// ClientCertificatesFromFiles sets client certificates from files
func (r *Request) ClientCertificatesFromFiles(certFile, keyFile, caFile string) (*Request, error) {
	clientCert, clientKey, caCert, err := readClientCertificateFiles(certFile, keyFile, caFile)
	if err != nil {
		return r, err
	}

	return r.ClientCertificates(clientCert, clientKey, caCert)
}

// ClientCertificatesFromInstance sets the client certificates from a cert instance
func (r *Request) ClientCertificatesFromInstance(clientCertWithKey tls.Certificate, caChain []*x509.Certificate) (*Request, error) {
	tlsConfig, err := setupClientCertificate(clientCertWithKey, caChain, r.client.TLSConfig)

	if err != nil {
		return r, err
	}

	r.client.TLSConfig = tlsConfig

	return r, nil
}

// QueryParams sets the URL / query parameters for the client call
// Note: Any existing URL / query parameters are overwritten / removed
func (r *Request) QueryParams(queryParams Params) *Request {
	r.queryParams = queryParams
	return r
}

// Headers sets the headers for the client call
// Note: Any existing headers are overwritten / removed
func (r *Request) Headers(headers Params) *Request {
	r.headers = headers
	return r
}

// Body sets the body for the client call
// Note: Any existing body is overwritten
func (r *Request) Body(body []byte) *Request {
	r.body = body
	return r
}

// Jar sets an optional cookie jar for the client call
// Note: Any existing jar is overwritten
func (r *Request) Jar(jar *Jar) *Request {
	r.jar = jar
	return r
}

// Encode encodes and sets the body for the client call using an arbitrary encoder
func (r *Request) Encode(encoder Encoder) *Request {
	r.bodyEncoder = encoder
	return r
}

// EncodeJSON encodes and sets the body for the client call using JSON encoding
func (r *Request) EncodeJSON(v interface{}) *Request {
	r.bodyEncoder = JSONEncoder{v}
	return r
}

// EncodeYAML encodes and sets the body for the client call using YAML encoding
func (r *Request) EncodeYAML(v interface{}) *Request {
	r.bodyEncoder = YAMLEncoder{v}
	return r
}

// EncodeXML encodes and sets the body for the client call using XML encoding
func (r *Request) EncodeXML(v interface{}) *Request {
	r.bodyEncoder = XMLEncoder{v}
	return r
}

// ParseFn sets a generic parsing function for the result of the client call
func (r *Request) ParseFn(parseFn func(*fasthttp.Response) error) *Request {
	r.parseFn = parseFn
	return r
}

// ParseJSON parses the result of the client call as JSON
func (r *Request) ParseJSON(v interface{}) *Request {
	r.parseFn = ParseJSON(v)
	return r
}

// ParseYAML parses the result of the client call as YAML
func (r *Request) ParseYAML(v interface{}) *Request {
	r.parseFn = ParseYAML(v)
	return r
}

// ParseXML parses the result of the client call as XML
func (r *Request) ParseXML(v interface{}) *Request {
	r.parseFn = ParseXML(v)
	return r
}

// ErrorFn sets a parsing function for results not handled by ParseFn
func (r *Request) ErrorFn(errorFn func(*fasthttp.Response) error) *Request {
	r.errorFn = errorFn
	return r
}

// Delay sets an artificial delay for the client call
func (r *Request) Delay(delay time.Duration) *Request {
	r.delay = delay
	return r
}

// ModifyFastHTTPClient executes any function / allows setting parameters of the
// underlying FastHTTP client before the actual request is made
func (r *Request) ModifyFastHTTPClient(fn func(*fasthttp.Client)) *Request {
	r.httpClientFunc = fn
	return r
}

// ModifyRequest allows the caller to call any methods or other functions on the
// http.Request prior to execution of the call
func (r *Request) ModifyRequest(fn func(*fasthttp.Request) error) *Request {
	r.httpRequestFunc = fn
	return r
}

// AcceptedResponseCodes defines a set of accepted HTTP response codes for the
// client call
func (r *Request) AcceptedResponseCodes(acceptedResponseCodes []int) *Request {
	r.acceptedResponseCodes = acceptedResponseCodes
	return r
}

func retryTimer(interval time.Duration) error {
	timer := time.NewTimer(interval)
	<-timer.C
	return nil
}

func retryTimerWithContext(ctx context.Context) func(time.Duration) error {
	return func(interval time.Duration) error {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		return nil
	}
}

func (r *Request) run(retryTimerFn func(interval time.Duration) error, req *fasthttp.Request) error {

	// Handle retry options / parameter
	retryErrFn, err := r.setRetryHandling()
	if err != nil {
		return err
	}

	// Prepare the actual request
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	if r.timeout > 0 {
		req.SetTimeout(r.timeout)
	}

	// Delay the request (if delay is set)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	err = r.client.DoRedirects(req, resp, 32)
	for i := 0; retryErrFn(r, resp, err) && i < len(r.retryIntervals); i++ {

		// If a retry event handler exists, trigger it
		if r.retryEventFn != nil {
			r.retryEventFn(i+1, resp, err)
		}
		// Wait until the retry inteval has elapsed or the context was cancelled
		// In both cases simply continue and have the client handle the (potentially
		// cancelled) context (that way the same error is returned no matter _when_
		// the context was cancelled)
		if err = retryTimerFn(r.retryIntervals[i]); err != nil {
			return err
		}

		// if err = r.setBody(req); err != nil {
		// 	return fmt.Errorf("failed to set request body: %w", err)
		// }
		err = r.client.DoRedirects(req, resp, 32)
		//err = r.client.Do(req, resp)
		// if err == nil && !isAnyOf(resp.StatusCode(), r.acceptedResponseCodes) {
		// 	err = fmt.Errorf("unexpected return code %d", resp.StatusCode())
		// }
	}
	if retryErrFn(r, resp, err) {
		if err != nil {
			return err
		}
	}

	return r.handleResponse(resp)
}

// Run executes a request
func (r *Request) Run() error {

	req, err := r.populate()
	if err != nil {
		return err
	}
	defer fasthttp.ReleaseRequest(req)

	return r.run(retryTimer, req)
}

// RunWithContext executes a request using a specific context
func (r *Request) RunWithContext(ctx context.Context, timeout time.Duration) error {

	req, err := r.populate()
	if err != nil {
		return err
	}
	req.SetTimeout(timeout)
	defer fasthttp.ReleaseRequest(req)

	return r.run(retryTimerWithContext(ctx), req)
}

var (
	errorEncodedRawBodyCollision = errors.New("cannot use both body encoding and raw body content")
)

func (r *Request) populate() (*fasthttp.Request, error) {

	// Early breakout in case no accepted responses have been defined
	if len(r.acceptedResponseCodes) == 0 {
		return nil, errors.New("no accepted HTTP response codes set, considering request to be failed")
	}

	uri := fasthttp.AcquireURI()
	if err := uri.Parse(nil, []byte(r.uri)); err != nil {
		fasthttp.ReleaseURI(uri)
		return nil, err
	}

	// u, err := url.Parse(r.uri)
	// if err != nil {
	// 	return fmt.Errorf("failed to parse request URI: %w", err)
	// }

	// r.client.Addr = u.Host
	// if u.Scheme == "https" {
	// 	r.client.IsTLS = true
	// }

	// Initialize new fasthttp.Request
	req := fasthttp.AcquireRequest()
	req.SetURI(uri)

	// If an explicit host override was provided it, set it
	if r.host != "" {
		req.SetHost(r.host)
	} else {
		req.SetHost(string(uri.Host()))
	}

	fasthttp.ReleaseURI(uri)
	req.Header.SetMethod(r.method)

	// req.SetRequestURI("/" + u.Path + "?" + u.RawQuery)
	// if u.Scheme == "https" {
	// 	// r.client.IsTLS = true
	// 	req.URI().SetScheme(u.Scheme)
	// 	req.URI().SetHost(u.Host)
	// }

	if err := r.setBody(req); err != nil {
		fasthttp.ReleaseRequest(req)
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	// Notify the server that the connection should be closed after completion of
	// the request
	// req.SetConnectionClose()

	// If requested, set authentication
	if r.httpAuthFunc != nil {
		r.httpAuthFunc(req)
	}

	// If URL parameters were provided, assign them to the request
	if r.queryParams != nil {
		q := req.URI().QueryArgs()
		for key, val := range r.queryParams {
			q.Set(key, val)
		}
	}

	// If headers were provided, assign them to the request
	if r.headers != nil {
		for key, val := range r.headers {
			req.Header.Set(key, val)
		}
	}

	// If a cookie jar was provided, set the cookies
	if r.jar != nil {
		r.jar.populateRequest(req)
	}

	if r.httpClientFunc != nil {
		r.httpClientFunc(r.client)
	}

	if r.httpRequestFunc != nil {
		err := r.httpRequestFunc(req)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			return nil, err
		}
	}

	return req, nil
}

func (r *Request) handleResponse(resp *fasthttp.Response) error {

	// Check if the query was successful
	if !isAnyOf(resp.StatusCode(), r.acceptedResponseCodes) {
		if r.errorFn != nil {
			return r.errorFn(resp)
		}

		// buf := new(bytes.Buffer)
		// if _, err := io.Copy(buf, resp.Body); err != nil {
		// 	return fmt.Errorf("failed to load body into buffer for error handling: %w", err)
		// }

		// Attempt to decode a generic JSON error from the response body
		var extraErr HTTPError
		if err := jsoniter.Unmarshal(resp.Body(), &extraErr); err == nil {
			return fmt.Errorf("%d %s [%.512s]", resp.StatusCode(), resp.Header.StatusMessage(), fmt.Sprintf("code=%d, message=%v", extraErr.Code, extraErr.Message))
		}

		// Attempt to decode the response body directly
		return fmt.Errorf("%d %s [body=%.512s]", resp.StatusCode(), resp.Header.StatusMessage(), string(resp.Body()))
	}

	// if fasthttp.StatusCodeIsRedirect(resp.StatusCode()) {
	// 	location := resp.Header.Peek("Location")
	// 	req.SetRequestURI(getRedirectURL(r.uri, location, req.DisableRedirectPathNormalizing))
	// 	goto doRequest
	// }

	// If a parsing function was provided, execute it
	if r.parseFn != nil {
		return r.parseFn(resp)
	}

	return nil
}

func (r *Request) setBody(req *fasthttp.Request) (err error) {

	// If requested, parse the requst body using the specified encoder
	bodyBytes := r.body
	if r.bodyEncoder != nil {
		if len(r.body) > 0 {
			return errorEncodedRawBodyCollision
		}

		bodyBytes, err = r.bodyEncoder.Encode()
		if err != nil {
			return fmt.Errorf("error encoding body: %w", err)
		}
		req.Header.Set("Content-Type", r.bodyEncoder.ContentType())
	}

	contentLength := len(bodyBytes)

	// From net/http:
	//
	// 	If body is of type *bytes.Buffer, *bytes.Reader, or *strings.Reader, the returned request's ContentLength is set to its exact value
	// 	(instead of -1), GetBody is populated (so 307 and 308 redirects can replay the body), and Body is set to NoBody if the ContentLength
	// 	is 0.
	// req.Body = http.NoBody
	if contentLength > 0 {
		// req.Set ContentLength = int64(contentLength)

		// If a delay was requested, assign a delayed reader
		// if r.delay > 0 {
		// 	dr := newDelayedReader(bodyBytes, r.delay)

		// 	req.GetBody = func() (io.ReadCloser, error) {
		// 		snapshot := newDelayedReader(bytes.NewReader(bodyBytes), r.delay)
		// 		return io.NopCloser(snapshot), nil
		// 	}
		// 	req.Body = io.NopCloser(dr)
		// 	return nil
		// }

		// buf := bytes.NewReader(bodyBytes)

		// req.GetBody = func() (io.ReadCloser, error) {
		// 	return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		// }
		req.SetBody(bodyBytes)
	}

	return nil
}

// func (r *Request) clientDo(ctx context.Context, req *fasthttp.Request, resp *fasthttp.Response) error {
// 	errChan := make(chan error)

// 	var wg sync.WaitGroup
// 	wg.Add(1)
// 	go func(req *fasthttp.Request, resp *fasthttp.Response) {
// 		errChan <- r.client.DoRedirects(req, resp, 32)
// 		close(errChan)
// 		wg.Done()
// 	}(req, resp)

// 	select {
// 	case <-ctx.Done():
// 		fmt.Println("CTX DONE")
// 		wg.Wait()
// 		return ctx.Err()
// 	case err := <-errChan:
// 		fmt.Println("ERR", err)
// 		wg.Wait()
// 		return err
// 	}
// }

////////////////////////////////////////////////////////////////////////////////

func (r *Request) setRetryHandling() (RetryErrFn, error) {

	// Check if provided parameters / options are consistent
	if len(r.retryIntervals) == 0 {
		if r.retryErrFn != nil || r.retryEventFn != nil {
			return nil, fmt.Errorf("cannot use RetryBackOffErrFn() [used: %v] / RetryEventFn() [used: %v] without providing intervals via RetryBackOff()",
				r.retryErrFn != nil,
				r.retryEventFn != nil)
		}
	}

	// Set retry / back-off function to use
	retryErrFn := defaultRetryErrFn
	if r.retryErrFn != nil {
		retryErrFn = r.retryErrFn
	}

	return retryErrFn, nil
}

// type delayReader struct {
// 	reader     io.Reader
// 	wasDelayed bool
// 	delay      time.Duration
// }

// func newDelayedReader(data []byte, delay time.Duration) *delayReader {
// 	return &delayReader{reader: reader, delay: delay}
// }

// func (a *delayReader) Read(p []byte) (int, error) {
// 	if !a.wasDelayed {
// 		time.Sleep(a.delay)
// 		a.wasDelayed = true
// 	}

// 	return a.reader.Read(p)
// }

func isAnyOf(val int, ref []int) bool {
	for _, v := range ref {
		if v == val {
			return true
		}
	}

	return false
}

func getRedirectURL(baseURL string, location []byte, disablePathNormalizing bool) string {
	u := fasthttp.AcquireURI()
	u.Update(baseURL)
	u.UpdateBytes(location)
	u.DisablePathNormalizing = disablePathNormalizing
	redirectURL := u.String()
	fasthttp.ReleaseURI(u)
	return redirectURL
}
