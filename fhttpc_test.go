package fhttpc

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v3"
)

type testCase struct {
	expectedStatusCode int
	requestBody        []byte
	responseBody       []byte
	responseFn         func(resp *fasthttp.Response) error
	errorFn            func(resp *fasthttp.Response) error

	queryParams Params
	headers     Params

	expectedError string

	hostName string

	respDelay time.Duration
}

type testStruct struct {
	Message string
	Status  int
}

const (
	helloWorldString = "Hello, world! 你好世界 😊😎"
	helloWorldJSON   = `{"status": 200, "message": "Hello, world! 你好世界 😊😎"}`
	helloWorldYAML   = `---
status: 200
message: "Hello, world! 你好世界 \U0001F60A\U0001F60E"
`
	helloWorldXML = `<?xml version="1.0" encoding="UTF-8"?>
<testStruct>
  <Message>Hello, world! 你好世界 😊😎</Message>
  <Status>200</Status>
</testStruct>
`
)

var (
	httpEndpoint  = "http://api.example.org"
	httpsEndpoint = "https://api.example.org"
)

func TestInvalidRequest(t *testing.T) {
	if err := New("", "").Run(); err == nil || err.Error() != `missing port in address` {
		t.Fatalf("Unexpected success creating invalid request: %s", err)
	}
	if err := New("😊", "").Run(); err == nil || err.Error() != `missing port in address` {
		t.Fatalf("Unexpected success creating invalid request: %s", err)
	}
	if err := New("", "NOTVALID").Run(); err == nil || err.Error() != `missing port in address` {
		t.Fatalf("Unexpected success creating invalid request: %s", err)
	}
	if err := New(fasthttp.MethodGet, "").EncodeJSON(struct{}{}).Body([]byte{0}).Run(); err == nil || !errors.Is(err, errorEncodedRawBodyCollision) {
		t.Fatalf("Unexpected success creating invalid request: %s", err)
	}
}

func TestTimeout(t *testing.T) {

	// Define a URI that safely won't exist on localhost
	uri := "http://127.0.0.1/uiatbucacajdahgsdkjasdgcagagd/timeout"

	// Apparently Windows has an issue with handling the context with very short timeouts, sporadically
	// yielding an i/o timeout error at the dial stage instead of the expected "context deadline exceeded"
	// Only workaround so far is to slightly increase the timeout value.
	// See https://github.com/fako1024/httpc/issues/22
	testTimeout := time.Nanosecond
	if runtime.GOOS == "windows" {
		testTimeout = 100 * time.Millisecond
	}

	t.Run("with-timeout-method", func(t *testing.T) {

		// Define request with very low timeout (a mocked delay does not trigger
		// the deadline excess)
		req := NewWithClient(fasthttp.MethodGet, uri, &fasthttp.Client{}).Timeout(testTimeout)

		// Execute the request
		if err := req.Run(); err == nil || err.Error() != "timeout" {
			t.Fatal(err)
		}
	})

	t.Run("with-context", func(t *testing.T) {

		// Only the request, as we are using the context for timeout
		req := NewWithClient(fasthttp.MethodGet, uri, &fasthttp.Client{}).RetryBackOff(Intervals{
			10 * time.Millisecond,
			10 * time.Millisecond,
			10 * time.Millisecond})

		// Define very low timeout (a mocked delay does not trigger
		// the deadline excess)
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		// Execute the request
		if err := req.RunWithContext(ctx, 100*time.Millisecond); err == nil || err.Error() != "context deadline exceeded" {
			t.Fatal(err)
		}
	})
}

func TestCancelContext(t *testing.T) {

	// Define a URI that safely won't exist on localhost
	uri := "http://127.0.0.1/uiatbucacajdahgsdkjasdgcagagd/cancelcontext"

	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		expectedStatusCode: fasthttp.StatusInternalServerError,
		respDelay:          20 * time.Millisecond,
	})
	defer mock.Close()

	// Only the request, as we are using the context for timeout
	req := New(fasthttp.MethodGet, uri).RetryBackOff(Intervals{
		20 * time.Millisecond,
		20 * time.Millisecond,
		20 * time.Millisecond,
		20 * time.Millisecond,
		20 * time.Millisecond,
		20 * time.Millisecond,
	})
	req.client = mock.Client()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the request after a second
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Execute the request
	start := time.Now()
	if err := req.RunWithContext(ctx, 5*time.Second); err == nil || err.Error() != "context canceled" {
		t.Fatal(err)
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("request with cancelled context took longer than expected (%v)", elapsed)
	}
}

func TestRetryInvalidOptions(t *testing.T) {
	uri := joinURI(httpsEndpoint, "invalidretries")
	if err := New(fasthttp.MethodPut, uri).
		RetryBackOffErrFn(func(req *Request, resp *fasthttp.Response, err error) bool { return true }).
		Body([]byte(helloWorldString)).Run(); err == nil || err.Error() != "cannot use RetryBackOffErrFn() [used: true] / RetryEventFn() [used: false] without providing intervals via RetryBackOff()" {
		t.Fatal(err)
	}
	if err := New(fasthttp.MethodPut, uri).
		RetryEventFn(func(i int, r *fasthttp.Response, err error) {}).
		Body([]byte(helloWorldString)).Run(); err == nil || err.Error() != "cannot use RetryBackOffErrFn() [used: false] / RetryEventFn() [used: true] without providing intervals via RetryBackOff()" {
		t.Fatal(err)
	}
	if err := New(fasthttp.MethodPut, uri).
		RetryBackOffErrFn(func(req *Request, resp *fasthttp.Response, err error) bool { return true }).
		RetryEventFn(func(i int, r *fasthttp.Response, err error) {}).
		Body([]byte(helloWorldString)).Run(); err == nil || err.Error() != "cannot use RetryBackOffErrFn() [used: true] / RetryEventFn() [used: true] without providing intervals via RetryBackOff()" {
		t.Fatal(err)
	}
}

func TestRetries(t *testing.T) {
	uri := joinURI(httpEndpoint, "retries")
	intervals := Intervals{10 * time.Millisecond, 15 * time.Millisecond, 20 * time.Millisecond}
	var sumIntervals time.Duration
	for i := 0; i < len(intervals); i++ {
		sumIntervals += intervals[i]
	}

	// Set up a mock matcher
	nTries := 0
	mock := NewMock(fasthttp.MethodPut, uri, testCase{
		expectedStatusCode: fasthttp.StatusOK,
	}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if string(ctx.Request.Body()) != helloWorldString {
			return false, fmt.Errorf("invalid body on attempt %d: %s", nTries, string(ctx.Request.Body()))
		}

		nTries++
		if nTries != 4 {
			return false, nil
		}
		return true, nil
	})
	defer mock.Close()

	retryResponses, retryErrs := make([]*fasthttp.Response, 0), make([]error, 0)
	req := New(fasthttp.MethodPut, uri).
		RetryBackOff(intervals).
		RetryEventFn(func(i int, r *fasthttp.Response, err error) {
			retryResponses = append(retryResponses, r)
			retryErrs = append(retryErrs, err)
		}).
		Body([]byte(helloWorldString))
	req.client = mock.Client()

	start := time.Now()
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
	if timeTaken := time.Since(start); timeTaken < sumIntervals {
		t.Fatalf("too short duration using Retry(): %v (expected >%v)", timeTaken, sumIntervals)
	}

	start = time.Now()
	if err := New(fasthttp.MethodPut, joinURI(httpEndpoint, "doesnotexist")).RetryBackOff(intervals).Body([]byte(helloWorldString)).Run(); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("unexpected success using Retry(): %s", err)
	}
	if timeTaken := time.Since(start); timeTaken < sumIntervals {
		t.Fatalf("too short duration using Retry(): %v", timeTaken)
	}
	if len(retryResponses) != len(intervals) {
		t.Fatalf("unexpected number of retry event handler responses (want %d, have %d)", len(intervals), len(retryResponses))
	}
	if len(retryErrs) != len(intervals) {
		t.Fatalf("unexpected number of retry event handler errors (want %d, have %d)", len(intervals), len(retryErrs))
	}
}

func TestRetriesErrorFn(t *testing.T) {
	uri := joinURI(httpEndpoint, "sdfhgajhdsd")
	intervals := Intervals{10 * time.Millisecond, 15 * time.Millisecond, 20 * time.Millisecond}
	var sumIntervals time.Duration
	for i := 0; i < len(intervals); i++ {
		sumIntervals += intervals[i]
	}

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodPut, uri, testCase{
		expectedStatusCode: fasthttp.StatusOK,
	}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if string(ctx.Request.Body()) != helloWorldString {
			return false, fmt.Errorf("invalid body: %s", string(ctx.Request.Body()))
		}

		return true, nil
	}).Reply(fasthttp.StatusBadRequest)
	defer mock.Close()

	retryResponses, retryErrs := make([]*fasthttp.Response, 0), make([]error, 0)
	req := New(fasthttp.MethodPut, uri).
		RetryBackOff(intervals).
		RetryBackOffErrFn(func(r *Request, resp *fasthttp.Response, err error) bool {
			return err != nil || resp.StatusCode() == 500
		}).
		Body([]byte(helloWorldString))
	req.client = mock.Client()

	start := time.Now()
	if err := req.Run(); err == nil || err.Error() != "400 Bad Request [body=]" {
		t.Fatalf("unexpected error response: %s", err)
	}
	if timeTaken := time.Since(start); timeTaken >= sumIntervals {
		t.Fatalf("too long duration using Retry(): %v", timeTaken)
	}

	start = time.Now()
	if err := New(fasthttp.MethodPut, joinURI(httpsEndpoint, "doesnotexist")).
		RetryBackOff(intervals).
		RetryEventFn(func(i int, r *fasthttp.Response, err error) {
			retryResponses = append(retryResponses, r)
			retryErrs = append(retryErrs, err)
		}).
		RetryBackOffErrFn(func(r *Request, resp *fasthttp.Response, err error) bool {
			return err != nil || resp.StatusCode() == 400
		}).
		Body([]byte(helloWorldString)).Run(); err == nil {
		t.Fatalf("unexpected success using Retry()")
	}
	if timeTaken := time.Since(start); timeTaken < sumIntervals {
		t.Fatalf("too short duration using Retry(): %v", timeTaken)
	}
	if len(retryResponses) != len(intervals) {
		t.Fatalf("unexpected number of retry event handler responses (want %d, have %d)", len(intervals), len(retryResponses))
	}
	if len(retryErrs) != len(intervals) {
		t.Fatalf("unexpected number of retry event handler errors (want %d, have %d)", len(intervals), len(retryErrs))
	}
}

func TestMultiMock(t *testing.T) {

	mock, err := NewMultiMock(
		NewMock(fasthttp.MethodGet, httpEndpoint+"/first", testCase{}).Reply(fasthttp.StatusOK),
		NewMock(fasthttp.MethodGet, httpEndpoint+"/second", testCase{}).Reply(fasthttp.StatusOK),
	)
	if err != nil {
		t.Fatalf("failed to instantiate MultiMock: %s", err)
	}
	defer mock.Close()

	req1 := New(fasthttp.MethodGet, httpEndpoint+"/first")
	req1.client = mock.Client()
	if err := req1.Run(); err != nil {
		t.Fatal(err)
	}

	req2 := New(fasthttp.MethodGet, httpEndpoint+"/second")
	req2.client = mock.Client()
	if err := req2.Run(); err != nil {
		t.Fatal(err)
	}

}

// func TestSlashRedirectHandling(t *testing.T) {
// 	uri := joinURI(httpsEndpoint, "original")
// 	redirectURI := uri + "/"

// 	// Set up a mock matcher
// 	g := gock.New(uri)
// 	g.Post(path.Base(uri)).
// 		Reply(http.StatusTemporaryRedirect).
// 		SetHeader("Location", redirectURI)

// 	gRedir := gock.New(redirectURI)
// 	gRedir.Persist()
// 	gRedir.Post(path.Base(uri) + "/").
// 		Reply(http.StatusOK).
// 		Body(bytes.NewBufferString(`{"status":42,"msg":"JSON String"}`))

// 	reqBody := testStruct{
// 		Status:  42,
// 		Message: "JSON String",
// 	}

// 	var respBody = struct {
// 		Status  int    `json:"status"`
// 		Message string `json:"msg"`
// 	}{}

// 	req := New(fasthttp.MethodPost, uri).EncodeJSON(reqBody).ParseJSON(&respBody)
// 	gock.InterceptClient(req.client)
// 	defer gock.RestoreClient(req.client)

// 	if err := req.Run(); err != nil {
// 		t.Fatal(err)
// 	}

// 	if respBody.Status != reqBody.Status || respBody.Message != reqBody.Message {
// 		t.Fatal("response body doesn't match what should be returned by redirect endpoint")
// 	}
// }

func TestRedirectHandling(t *testing.T) {
	uri := joinURI(httpEndpoint, "original")
	redirectURI := joinURI(httpEndpoint, "redirected")

	// Set up a mock matcher
	mock, err := NewMultiMock(
		NewMock(fasthttp.MethodPut, uri, testCase{}).Reply(fasthttp.StatusPermanentRedirect).
			RespHeaders(Params{
				"Location": redirectURI,
			}),
		NewMock(fasthttp.MethodPut, redirectURI, testCase{
			responseBody: []byte(`{"status":42,"msg":"JSON String"}`),
		}).Reply(fasthttp.StatusOK),
	)
	if err != nil {
		t.Fatalf("failed to instantiate MultiMock: %s", err)
	}
	defer mock.Close()

	reqBody := testStruct{
		Status:  42,
		Message: "JSON String",
	}

	var respBody = struct {
		Status  int    `json:"status"`
		Message string `json:"msg"`
	}{}

	req := New(fasthttp.MethodPut, uri).EncodeJSON(reqBody).ParseJSON(&respBody)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	if respBody.Status != reqBody.Status || respBody.Message != reqBody.Message {
		t.Fatal("response body doesn't match what should be returned by redirect endpoint")
	}
}

func TestBodyCopy(t *testing.T) {
	uri := joinURI(httpEndpoint, "bodyCopy")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		responseBody: []byte(helloWorldString),
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	buf := bytes.NewBuffer(nil)
	req := New(fasthttp.MethodGet, uri).ParseFn(Copy(buf))
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	if buf.String() != helloWorldString {
		t.Fatalf("invalid response, want `%s`, have `%s", helloWorldString, buf.String())
	}
}

func TestBodySet(t *testing.T) {
	uri := joinURI(httpEndpoint, "bodySet")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		responseBody: []byte(helloWorldString),
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	buf := make([]byte, 0)
	req := New(fasthttp.MethodGet, uri).ParseFn(Set(&buf))
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
	if string(buf) != helloWorldString {
		t.Fatalf("invalid response, want `%s`, have `%s", helloWorldString, string(buf))
	}
}

type gobEncoder struct {
	v interface{}
}

// Encode fulfills the Encoder interface, performing the actual encoding
func (e gobEncoder) Encode() ([]byte, error) {
	w := new(bytes.Buffer)
	if err := gob.NewEncoder(w).Encode(e.v); err != nil {
		return nil, err
	}

	return w.Bytes(), nil
}

// Encode fulfills the Encoder interface, providing the required content-type header
func (e gobEncoder) ContentType() string {
	return "application/custom-type"
}

func TestGenericEncoding(t *testing.T) {
	uri := joinURI(httpEndpoint, "genericEncoding")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if contentType := ctx.Request.Header.Peek("Content-Type"); string(contentType) != "application/custom-type" {
			return false, fmt.Errorf("unexpected content-type: %s", contentType)
		}

		buf := bytes.NewBuffer(ctx.Request.Body())
		var parsedBody testStruct
		if err := gob.NewDecoder(buf).Decode(&parsedBody); err != nil {
			return false, err
		}

		if parsedBody.Status != 42 || parsedBody.Message != "JSON String" {
			return false, fmt.Errorf("unexpected content of parsed content: %v", parsedBody)
		}

		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	reqBody := testStruct{
		Status:  42,
		Message: "JSON String",
	}

	req := New(fasthttp.MethodGet, uri).Encode(gobEncoder{reqBody})
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONRequest(t *testing.T) {
	t.Run("no_duplicate_header", func(t *testing.T) {
		testJSONRequest(false, t)
	})
	t.Run("duplicate_header", func(t *testing.T) {
		testJSONRequest(true, t)
	})
}

func testJSONRequest(duplicateHeader bool, t *testing.T) {
	uri := joinURI(httpEndpoint, "jsonRequest")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if contentType := ctx.Request.Header.Peek("Content-Type"); string(contentType) != "application/json" {
			return false, fmt.Errorf("unexpected content-type: %s", contentType)
		}

		if contentTypeValues := ctx.Request.Header.PeekAll("Content-Type"); len(contentTypeValues) != 1 {
			return false, fmt.Errorf("unexpected content-type values: %s", contentTypeValues)
		}

		var parsedBody testStruct
		if err := jsoniter.Unmarshal(ctx.Request.Body(), &parsedBody); err != nil {
			return false, err
		}

		if parsedBody.Status != 42 || parsedBody.Message != "JSON String" {
			return false, fmt.Errorf("unexpected content of parsed content: %v", parsedBody)
		}

		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	reqBody := testStruct{
		Status:  42,
		Message: "JSON String",
	}

	req := New(fasthttp.MethodGet, uri).EncodeJSON(reqBody)
	if duplicateHeader {
		req.Headers(Params{
			"Content-type": "application/json",
		})
	}
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONParser(t *testing.T) {
	uri := joinURI(httpEndpoint, "jsonParsing")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		responseBody: []byte(helloWorldJSON),
	}).Reply(fasthttp.StatusOK).RespHeaders(Params{"Content-Type": "application/json"})
	defer mock.Close()

	var parsedResult testStruct
	req := New(fasthttp.MethodGet, uri).ParseJSON(&parsedResult)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	if parsedResult.Status != 200 || parsedResult.Message != helloWorldString {
		t.Fatalf("unexpected content of parsed result: %v", parsedResult)
	}
}

func TestYAMLRequest(t *testing.T) {
	t.Run("no_duplicate_header", func(t *testing.T) {
		testYAMLRequest(false, t)
	})
	t.Run("duplicate_header", func(t *testing.T) {
		testYAMLRequest(true, t)
	})
}

func testYAMLRequest(duplicateHeader bool, t *testing.T) {
	uri := joinURI(httpEndpoint, "yamlRequest")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if contentType := ctx.Request.Header.Peek("Content-Type"); string(contentType) != "application/yaml" {
			return false, fmt.Errorf("unexpected content-type: %s", contentType)
		}

		if contentTypeValues := ctx.Request.Header.PeekAll("Content-Type"); len(contentTypeValues) != 1 {
			return false, fmt.Errorf("unexpected content-type values: %s", contentTypeValues)
		}

		var parsedBody testStruct
		if err := yaml.Unmarshal(ctx.Request.Body(), &parsedBody); err != nil {
			return false, err
		}

		if parsedBody.Status != 42 || parsedBody.Message != "YAML String" {
			return false, fmt.Errorf("unexpected content of parsed content: %v", parsedBody)
		}

		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	reqBody := testStruct{
		Status:  42,
		Message: "YAML String",
	}

	req := New(fasthttp.MethodGet, uri).EncodeYAML(reqBody)
	if duplicateHeader {
		req.Headers(Params{
			"Content-type": "application/yaml",
		})
	}
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLParser(t *testing.T) {
	uri := joinURI(httpEndpoint, "yamlParsing")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		responseBody: []byte(helloWorldYAML),
	}).Reply(fasthttp.StatusOK).RespHeaders(Params{"Content-Type": "application/yaml"})
	defer mock.Close()

	var parsedResult testStruct
	req := New(fasthttp.MethodGet, uri).ParseYAML(&parsedResult)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	if parsedResult.Status != 200 || parsedResult.Message != helloWorldString {
		t.Fatalf("unexpected content of parsed result: %v", parsedResult)
	}
}

func TestXMLRequest(t *testing.T) {
	t.Run("no_duplicate_header", func(t *testing.T) {
		testXMLRequest(false, t)
	})
	t.Run("duplicate_header", func(t *testing.T) {
		testXMLRequest(true, t)
	})
}

func testXMLRequest(duplicateHeader bool, t *testing.T) {
	uri := joinURI(httpEndpoint, "xmlRequest")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if contentType := ctx.Request.Header.Peek("Content-Type"); string(contentType) != "application/xml" {
			return false, fmt.Errorf("unexpected content-type: %s", contentType)
		}

		if contentTypeValues := ctx.Request.Header.PeekAll("Content-Type"); len(contentTypeValues) != 1 {
			return false, fmt.Errorf("unexpected content-type values: %s", contentTypeValues)
		}

		var parsedBody testStruct
		if err := xml.Unmarshal(ctx.Request.Body(), &parsedBody); err != nil {
			return false, err
		}

		if parsedBody.Status != 42 || parsedBody.Message != "XML String" {
			return false, fmt.Errorf("unexpected content of parsed content: %v", parsedBody)
		}

		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	reqBody := testStruct{
		Status:  42,
		Message: "XML String",
	}

	req := New(fasthttp.MethodGet, uri).EncodeXML(reqBody)
	if duplicateHeader {
		req.Headers(Params{
			"Content-type": "application/xml",
		})
	}
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestXMLParser(t *testing.T) {
	uri := joinURI(httpEndpoint, "xmlParsing")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		responseBody: []byte(helloWorldXML),
	}).Reply(fasthttp.StatusOK).RespHeaders(Params{"Content-Type": "application/xml"})
	defer mock.Close()

	var parsedResult testStruct
	req := New(fasthttp.MethodGet, uri).ParseXML(&parsedResult)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	if parsedResult.Status != 200 || parsedResult.Message != helloWorldString {
		t.Fatalf("unexpected content of parsed result: %v", parsedResult)
	}
}

func TestBasicAuth(t *testing.T) {
	uri := joinURI(httpEndpoint, "authBasic")

	user, password := "testuser", "testpassword"

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		headers: map[string]string{
			"Authorization": "Basic " + base64.RawStdEncoding.EncodeToString([]byte(user+":"+password)),
		},
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	req := New(fasthttp.MethodGet, uri).AuthBasic(user, password)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestBearerAuth(t *testing.T) {
	uri := joinURI(httpEndpoint, "authBearer")

	token := "testtoken"

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		headers: map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", token),
		},
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	req := New(fasthttp.MethodGet, uri).AuthBearer(token)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestTokenAuth(t *testing.T) {
	uri := joinURI(httpEndpoint, "authToken")

	prefix, token := "testprefix", "testtoken"

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		headers: map[string]string{
			"Authorization": fmt.Sprintf("%s %s", prefix, token),
		},
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	req := New(fasthttp.MethodGet, uri).AuthToken(prefix, token)
	req.client = mock.Client()

	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestModifyRequest(t *testing.T) {
	uri := joinURI(httpEndpoint, "modifyRequest")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{
		headers: map[string]string{
			"X-TEST": "test",
		},
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	t.Run("add-header", func(t *testing.T) {
		req := New(fasthttp.MethodGet, uri).ModifyRequest(func(req *fasthttp.Request) error {
			req.Header.Add("X-TEST", "test")
			return nil
		})
		req.client = mock.Client()

		for i := 0; i < 100; i++ {
			// Execute the request
			if err := req.Run(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReuse(t *testing.T) {

	uri := joinURI(httpEndpoint, "reuse")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	t.Run("normal", func(t *testing.T) {
		req := New(fasthttp.MethodGet, uri)
		req.client = mock.Client()

		for i := 0; i < 100; i++ {

			// Execute the request
			if err := req.Run(); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("with-context-timeout", func(t *testing.T) {
		req := New(fasthttp.MethodGet, uri)
		req.client = mock.Client()

		for i := 0; i < 100; i++ {
			func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				// Execute the request
				if err := req.RunWithContext(ctx, 10*time.Second); err != nil {
					t.Fatal(err)
				}
			}(t)
		}
	})
}

func TestClientDelay(t *testing.T) {

	uri := joinURI(httpEndpoint, "delay")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		return bytes.Equal(ctx.Request.Body(), []byte(helloWorldString)), nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	// Add client-side delay to the request
	start := time.Now()
	req := New(fasthttp.MethodGet, uri).Body([]byte(helloWorldString)).Delay(50 * time.Millisecond)
	req.client = mock.Client()

	// Execute the request
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}

	// Check the latency of the request
	if lat := time.Since(start); lat < 50*time.Millisecond {
		t.Fatalf("Delayed request unexpectedly too fast, latency %v", lat)
	}
}

func TestCookieJar(t *testing.T) {

	uri := joinURI(httpEndpoint, "cookies")

	jar := AcquireJar()
	defer ReleaseJar(jar)

	jar.SetCookie("test_cookie1", "lW9p2ku5iR2OjDHS69xa")
	jar.SetCookie("test_cookie2", "wBbycTsBM7yGIxURLKSp")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		countCookies := 0
		var err error
		ctx.Request.Header.VisitAllCookie(func(key, value []byte) {
			countCookies++
			if string(jar.Peek(string(key))) != string(value) {
				err = fmt.Errorf("mismatching cookie, want %s, have %s", string(jar.Peek(string(key))), string(value))
			}
		})

		if countCookies != jar.Len() {
			return false, fmt.Errorf("unexpected number of cookies")
		}
		if err != nil {
			return false, err
		}

		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	// Define request setting custom HTTP client parameters
	req := New(fasthttp.MethodGet, uri).Jar(jar)
	req.client = mock.Client()

	// Execute the request
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestModifyClient(t *testing.T) {

	uri := joinURI(httpEndpoint, "client_modification")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}, func(ctx *fasthttp.RequestCtx) (bool, error) {
		if string(ctx.UserAgent()) != "test-client-name" {
			return false, nil
		}
		return true, nil
	}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	// Define request setting a custom HTTP client parameter
	req := New(fasthttp.MethodGet, uri).ModifyFastHTTPClient(func(c *fasthttp.Client) {
		c.Name = "test-client-name"
	})
	req.client = mock.Client()

	// Execute the request
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidClientCertificates(t *testing.T) {

	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificates(nil, nil, nil); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificates([]byte{}, []byte{}, []byte{}); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificates([]byte{0}, []byte{1, 2}, []byte{3, 4, 5}); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles("", "", ""); err == nil {
		t.Fatal("Unexpected non-nil error")
	}

	tmpClientCertFile, err := genTempFile([]byte(testClientCert))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpClientCertFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()
	tmpClientKeyFile, err := genTempFile([]byte(testClientKey))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpClientKeyFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()
	tmpCACertFile, err := genTempFile([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpCACertFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()

	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles("/tmp/JADGSYDYhsdgayawjdas", "", ""); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles("", "/tmp/JADGSYDYhsdgayawjdas", ""); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles(tmpClientCertFile, "", ""); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles(tmpClientCertFile, tmpClientKeyFile, ""); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
	if _, err := New(fasthttp.MethodGet, "https://127.0.0.1:10001/").ClientCertificatesFromFiles(tmpClientCertFile, tmpClientKeyFile, tmpCACertFile); err == nil {
		t.Fatal("Unexpected non-nil error")
	}
}

func TestClientCertificates(t *testing.T) {

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, "https://127.0.0.1:10001/", testCase{}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	// Define request disabling certificate validation
	req := New(fasthttp.MethodGet, "https://127.0.0.1:10001/")
	req.client = mock.Client()
	req, err := req.SkipCertificateVerification().ClientCertificates([]byte(testClientCert), []byte(testClientKey), []byte(testCACert))
	if err != nil {
		t.Fatal(err)
	}

	// Execute the request
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestClientCertificatesFromFiles(t *testing.T) {

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, "https://127.0.0.1:10001/", testCase{}).Reply(fasthttp.StatusOK)
	defer mock.Close()

	tmpClientCertFile, err := genTempFile([]byte(testClientCert))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpClientCertFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()
	tmpClientKeyFile, err := genTempFile([]byte(testClientKey))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpClientKeyFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()
	tmpCACertFile, err := genTempFile([]byte(testCACert))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := os.Remove(tmpCACertFile); rerr != nil {
			t.Fatal(rerr)
		}
	}()

	// Define request disabling certificate validation
	req := New(fasthttp.MethodGet, "https://127.0.0.1:10001/")
	req.client = mock.Client()
	req, err = req.SkipCertificateVerification().ClientCertificatesFromFiles(tmpClientCertFile, tmpClientKeyFile, tmpCACertFile)
	if err != nil {
		t.Fatal(err)
	}

	// Execute the request
	if err := req.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedResponseCodes(t *testing.T) {

	if err := testResponseCode([]int{}, fasthttp.StatusOK); err == nil || err.Error() != "no accepted HTTP response codes set, considering request to be failed" {
		t.Fatalf("unexpected success for empty accepted response codes")
	}

	codes := []int{
		fasthttp.StatusOK,
		fasthttp.StatusGone,
		fasthttp.StatusLocked,
		fasthttp.StatusCreated,
		fasthttp.StatusAccepted,
		// fasthttp.StatusContinue, // TODO: This might need a special test
		fasthttp.StatusNoContent,
	}

	var acceptedCodes []int
	for i, acceptedCode := range codes {
		acceptedCodes = append(acceptedCodes, acceptedCode)

		for _, returnedCode := range codes[:i+1] {
			if err := testResponseCode(acceptedCodes, returnedCode); err != nil {
				t.Fatalf("unexpected failure for code %d, accepted codes %v: %s", returnedCode, acceptedCodes, err)
			}
		}

		for _, returnedCode := range codes[i+1:] {
			if err := testResponseCode(acceptedCodes, returnedCode); err == nil {
				t.Fatalf("unexpected success for code %d, accepted codes %v", returnedCode, acceptedCodes)
			}
		}
	}
}

func TestTable(t *testing.T) {

	var (
		parsedStruct testStruct
	)
	var testRequests = map[*Request]testCase{
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "simple_ok")): {
			expectedStatusCode: fasthttp.StatusOK,
		},
		New(fasthttp.MethodPost, joinURI(httpEndpoint, "simple_ok")): {
			expectedStatusCode: fasthttp.StatusOK,
		},
		New(fasthttp.MethodPut, joinURI(httpEndpoint, "simple_ok")): {
			expectedStatusCode: fasthttp.StatusOK,
		},
		New(fasthttp.MethodDelete, joinURI(httpEndpoint, "simple_ok")): {
			expectedStatusCode: fasthttp.StatusOK,
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "set_hostname")): {
			expectedStatusCode: fasthttp.StatusOK,
			hostName:           "api2.example.org",
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "simple_params")): {
			expectedStatusCode: fasthttp.StatusOK,
			queryParams: map[string]string{
				"param1": "DPZU3PILpO2vtoe0oRq6",
				"param2": "NvleFEzAcBzhMhvQSBKB 你好世界 😊😎",
			},
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "simple_headers")): {
			expectedStatusCode: fasthttp.StatusOK,
			headers: map[string]string{
				"X-TEST-HEADER-1": "sExavefMTeOVFu6LfLLN",
				"X-TEST-HEADER-2": "zHW4aaMhMJzrA5eJtahB 你好世界 😊😎",
			},
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "string_request")): {
			expectedStatusCode: fasthttp.StatusOK,
			requestBody:        []byte(helloWorldString),
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "string_response")): {
			expectedStatusCode: fasthttp.StatusOK,
			responseBody:       []byte(helloWorldString),
			responseFn: func(resp *fasthttp.Response) error {
				if string(resp.Body()) != helloWorldString {
					return fmt.Errorf("Unexpected response body string, want `%s`, have `%s`", helloWorldString, string(resp.Body()))
				}
				return nil
			},
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "json_response")): {
			expectedStatusCode: fasthttp.StatusOK,
			responseBody:       []byte(helloWorldJSON),
			responseFn:         ParseJSON(&parsedStruct),
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "yaml_response")): {
			expectedStatusCode: fasthttp.StatusOK,
			responseBody:       []byte(helloWorldYAML),
			responseFn:         ParseYAML(&parsedStruct),
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "xml_response")): {
			expectedStatusCode: fasthttp.StatusOK,
			responseBody:       []byte(helloWorldXML),
			responseFn:         ParseXML(&parsedStruct),
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "byte_response")): {
			expectedStatusCode: fasthttp.StatusOK,
			responseBody:       []byte(helloWorldString),
			responseFn: func(resp *fasthttp.Response) error {
				buf := new(bytes.Buffer)
				if err := Copy(buf)(resp); err != nil {
					return err
				}
				if buf.String() != helloWorldString {
					return fmt.Errorf("Unexpected body string: %s", buf.String())
				}
				return nil
			},
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "404_response")): {
			expectedStatusCode: fasthttp.StatusNotFound,
			expectedError:      "got 404",
			errorFn: func(resp *fasthttp.Response) error {
				return fmt.Errorf("got %d", resp.StatusCode())
			},
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "404_response_noFn")): {
			expectedStatusCode: fasthttp.StatusNotFound,
			expectedError:      "404 Not Found [body=]",
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "401_response_withJSON")): {
			expectedStatusCode: fasthttp.StatusUnauthorized,
			responseBody:       []byte(`{"code": 401, "message": "no authorization"}`),
			expectedError:      "401 Unauthorized [code=401, message=no authorization]",
		},
		New(fasthttp.MethodGet, joinURI(httpEndpoint, "401_response_withBody")).AcceptedResponseCodes([]int{fasthttp.StatusNoContent}): {
			expectedStatusCode: fasthttp.StatusUnauthorized,
			responseBody:       []byte("no authorization"),
			expectedError:      "401 Unauthorized [body=no authorization]",
		},
	}

	for k, v := range testRequests {
		t.Run(fmt.Sprintf("%s %s", k.method, k.uri), func(t *testing.T) {
			if err := runGenericRequest(k, v); err != nil {
				if v.expectedError != "" {
					if v.expectedError != err.Error() {
						t.Fatalf("error expected %s, got %s", v.expectedError, err.Error())
					}
				} else {
					t.Fatalf("Failed running test: %s", err)
				}
			}
		})
	}
}

func runGenericRequest(k *Request, v testCase) error {

	mock := NewMock(k.method, k.uri, v)
	defer mock.Close()

	// Execute and parse the result (if parsing function was provided)
	req := k.ParseFn(v.responseFn).ErrorFn(v.errorFn)
	req.client = mock.Client()

	if err := testGetters(req); err != nil {
		return fmt.Errorf("Getter validation failed: %w", err)
	}

	// If a hostname was provided, set it
	if v.hostName != "" {
		req.HostName(v.hostName)
	}

	// Handle query parameters
	if len(v.queryParams) > 0 {
		req.QueryParams(v.queryParams)
	}

	// Handle headers
	if len(v.headers) > 0 {
		req.Headers(v.headers)
	}

	// Handle request body
	if v.requestBody != nil {
		req.Body(v.requestBody)
	}

	// Execute the request
	return req.Run()
}

func testGetters(req *Request) error {

	if req.GetURI() != req.uri {
		return fmt.Errorf("Unexpected getter URI received")
	}

	if req.GetMethod() != req.method {
		return fmt.Errorf("Unexpected getter method received")
	}

	if !bytes.Equal(req.GetBody(), req.body) {
		return fmt.Errorf("Unexpected getter body received")
	}

	return nil
}

func testResponseCode(codes []int, returnCode int) error {

	uri := joinURI(httpEndpoint, "codes")

	// Set up a mock matcher
	mock := NewMock(fasthttp.MethodGet, uri, testCase{}).Reply(returnCode)
	defer mock.Close()

	// Define request disabling certificate validation
	req := New(fasthttp.MethodGet, uri).AcceptedResponseCodes(codes)
	req.client = mock.Client()

	// Execute the request
	return req.Run()
}

func joinURI(base, suffix string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	u.Path = path.Join(u.Path, suffix)
	return u.String()
}

func genTempFile(data []byte) (string, error) {

	tmpfile, err := os.CreateTemp("", "httpc_test")
	if err != nil {
		return "", err
	}
	if _, err := tmpfile.Write(data); err != nil {
		return "", err
	}
	if err := tmpfile.Close(); err != nil {
		return "", err
	}

	return tmpfile.Name(), nil
}

func TestNewWithClient(t *testing.T) {
	testClient := &fasthttp.Client{}
	r := NewWithClient(fasthttp.MethodGet, "/test", testClient)
	if r.client == nil {
		t.Fatal("client is nil")
	}
	if r.client != testClient {
		t.Fatal("client is != testClient")
	}

	r = NewWithClient(fasthttp.MethodGet, "/test", nil)
	if r.client == nil {
		t.Fatal("client is nil")
	}
}
