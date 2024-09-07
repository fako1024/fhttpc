package fhttpc

import (
	"encoding/base64"

	"github.com/valyala/fasthttp"
)

const authHeaderKey = "Authorization"

// AuthBasic sets parameters to perform basic authentication
func (r *Request) AuthBasic(user, password string) *Request {
	r.httpAuthFunc = func(req *fasthttp.Request) {
		req.Header.Set(authHeaderKey, "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+password)))
	}
	return r
}

// AuthToken sets parameters to perform any token-based authentication, setting
// "Authorization: <prefix> <token>"
func (r *Request) AuthToken(prefix, token string) *Request {
	r.httpAuthFunc = func(req *fasthttp.Request) {
		req.Header.Set(authHeaderKey, prefix+" "+token)
	}
	return r
}

// AuthBearer sets parameters to perform bearer token authentication, setting
// "Authorization: Bearer <token>"
func (r *Request) AuthBearer(token string) *Request {
	r.httpAuthFunc = func(req *fasthttp.Request) {
		req.Header.Set(authHeaderKey, "Bearer "+token)
	}
	return r
}
