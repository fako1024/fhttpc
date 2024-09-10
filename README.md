# A simple wrapper around the Go fasthttp client optimized for ease-of-use

[![Github Release](https://img.shields.io/github/release/fako1024/fhttpc.svg)](https://github.com/fako1024/fhttpc/releases)
[![GoDoc](https://godoc.org/github.com/fako1024/fhttpc?status.svg)](https://godoc.org/github.com/fako1024/fhttpc/)
[![Go Report Card](https://goreportcard.com/badge/github.com/fako1024/fhttpc)](https://goreportcard.com/report/github.com/fako1024/fhttpc)
[![Build/Test Status](https://github.com/fako1024/fhttpc/workflows/Go/badge.svg)](https://github.com/fako1024/fhttpc/actions?query=workflow%3AGo)
[![CodeQL](https://github.com/fako1024/fhttpc/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/fako1024/fhttpc/actions/workflows/codeql-analysis.yml)

This package wraps the Go [fasthttp](https://github.com/valyala/fasthttp) client, providing a simplified interaction model using method chaining and various additional capabilities.

## Features
- Simple, method chaining based interface for fasthttp client requests
- Simulation of request delays
- Customization of fasthttp client via functional parameter
- Back-Off-Retry concept to automatically retry requests if required

## Installation
```bash
go get -u github.com/fako1024/fhttpc
```

## Examples
#### Perform simple HTTP GET request
```go
err := fhttpc.New("GET", "http://example.org").Run()
if err != nil {
	log.Fatalf("error performing GET request: %s", err)
}
```

#### Perform HTTP GET request and parse the result as JSON into a struct
```go
var res = struct {
	Status int
	Message string
}{}
err := fhttpc.New("GET", "http://example.org").
	ParseJSON(&res).
	Run()
if err != nil {
	log.Fatalf("error performing GET request: %s", err)
}
```

#### Perform HTTPS POST request with a simple body, disabling certificate validation and copying the response to a bytes.Buffer
```go
buf := new(bytes.Buffer)
err := fhttpc.New("POST", "https://example.org").
	SkipCertificateVerification().
	Body([]byte{0x1, 0x2}).
	ParseFn(fhttpc.Copy(buf)).
	Run()

if err != nil {
    log.Fatalf("error performing POST request: %s", err)
}

fmt.Println(buf.String())
```

#### Perform HTTPS GET request (with query parameters + headers + basic auth)
```go
err := fhttpc.New("GET", "https://example.org").
	SkipCertificateVerification().
	QueryParams(fhttpc.Params{
		"param": "test",
	}).
	Headers(fhttpc.Params{
		"X-HEADER-TEST": "test",
	}).
	AuthBasic("username", "password").
	Run()

if err != nil {
	log.Fatalf("error performing GET request: %s", err)
}
```
