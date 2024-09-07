package fhttpc

import (
	"encoding/xml"
	"io"

	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// HTTPError represents an error that occurred while handling a request
// Identical to struct used in labstack/echo
type HTTPError struct {
	Code     int
	Message  interface{}
	Internal error // Stores the error returned by an external dependency
}

// Set sets the provided byte slice to the response body
func Set(b *[]byte) func(resp *fasthttp.Response) error {
	return func(resp *fasthttp.Response) error {
		*b = resp.Body()
		return nil
	}
}

// Copy copies the response body into any io.Writer
func Copy(w io.Writer) func(resp *fasthttp.Response) error {
	return func(resp *fasthttp.Response) error {
		_, err := w.Write(resp.Body())
		return err
	}
}

// ParseJSON parses the response body as JSON into a struct
func ParseJSON(v interface{}) func(resp *fasthttp.Response) error {
	return func(resp *fasthttp.Response) error {
		return jsoniter.Unmarshal(resp.Body(), v)
	}
}

// ParseYAML parses the response body as YAML into a struct
func ParseYAML(v interface{}) func(resp *fasthttp.Response) error {
	return func(resp *fasthttp.Response) error {
		return yaml.Unmarshal(resp.Body(), v)
	}
}

// ParseXML parses the response body as XML into a struct
func ParseXML(v interface{}) func(resp *fasthttp.Response) error {
	return func(resp *fasthttp.Response) error {
		return xml.Unmarshal(resp.Body(), v)
	}
}
