package fhttpc

import (
	"sync"

	"github.com/valyala/fasthttp"
)

var cookiePool = sync.Pool{
	New: func() interface{} {
		return &Jar{}
	},
}

// AcquireCookieJar returns an empty CookieJar object from pool
func AcquireJar() *Jar {
	return cookiePool.Get().(*Jar)
}

// ReleaseCookieJar returns CookieJar to the pool
func ReleaseJar(c *Jar) {
	for k, v := range *c {
		fasthttp.ReleaseCookie(v)
		delete(*c, k)
	}
	cookiePool.Put(c)
}

// Jar denotes a cookie jar
type Jar map[string]*fasthttp.Cookie

// Peek returns the value bytes of the cookie with the provided key (nil if it doesn't exist)
func (j *Jar) Peek(key string) []byte {
	cookie, ok := (*j)[key]
	if !ok {
		return nil
	}
	return cookie.Value()
}

// SetCookie sets a cookie in the jar
func (j *Jar) SetCookie(key, value string) {
	c, ok := (*j)[key]
	if !ok {
		c = fasthttp.AcquireCookie()
	}
	c.SetKey(key)
	c.SetValue(value)
	(*j)[key] = c
}

// Len returns the number of objects in the cookie jar
func (j *Jar) Len() int {
	return len(*j)
}

func (j *Jar) populateRequest(r *fasthttp.Request) {
	for _, c := range *j {
		r.Header.SetCookieBytesKV(c.Key(), c.Value())
	}
}
