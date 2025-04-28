package api

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"time"
)

func NewAuthedTransportWithBearer(inner http.RoundTripper, bearer string) http.RoundTripper {
	return &authedTransport{wrapped: inner, bearer: bearer}
}

func NewAuthedTransportWithToken(inner http.RoundTripper, token string) http.RoundTripper {
	return &authedTransport{wrapped: inner, token: token}
}

type authedTransport struct {
	bearer  string
	token   string
	wrapped http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTripper should not mutate the request except to close the body, and
	// should always close the request body whether or not there was an error.
	// See https://pkg.go.dev/net/http#RoundTripper.
	// This implementation based on https://github.com/golang/oauth2/blob/master/transport.go
	reqBodyClosed := false
	if req.Body != nil {
		defer func() {
			if !reqBodyClosed {
				req.Body.Close()
			}
		}()
	}

	reqCopy := req.Clone(req.Context())
	switch {
	case t.bearer != "":
		reqCopy.Header.Set("Authorization", "Bearer "+t.bearer)
	case t.token != "":
		reqCopy.Header.Set("Authorization", "Token "+t.token)
	}

	reqBodyClosed = true
	return t.wrapped.RoundTrip(reqCopy)
}

type logTransport struct {
	inner http.RoundTripper
}

func NewLogger(inner http.RoundTripper) http.RoundTripper {
	return &logTransport{inner}
}

func (t *logTransport) RoundTrip(in *http.Request) (out *http.Response, err error) {
	// Inspired by: github.com/motemen/go-loghttp
	if _, ok := os.LookupEnv("DEBUG"); !ok {
		return t.inner.RoundTrip(in)
	}

	log.Printf("--> %s %s", in.Method, in.URL)

	// Save these headers so we can redact Authorization.
	inCopy := in
	if in.Header != nil && in.Header.Get("authorization") != "" {
		inCopy = in.Clone(in.Context())
		inCopy.Header.Set("authorization", "<redacted>")
	}

	b, err := httputil.DumpRequestOut(inCopy, true)
	if err != nil {
		log.Printf("Failed to dump request %s %s: %v", in.Method, in.URL, err)
	}
	if b := string(b); b != "" {
		log.Println(b)
	}

	start := time.Now()
	out, err = t.inner.RoundTrip(in)
	duration := time.Since(start)
	if err != nil {
		log.Printf("<-- %v %s %s (%s)", err, in.Method, in.URL, duration)
	}

	if out == nil {
		return
	}
	msg := fmt.Sprintf("<-- %d", out.StatusCode)
	if out.Request != nil {
		msg = fmt.Sprintf("%s %s", msg, out.Request.URL)
	}
	log.Printf("%s (%s)", msg, duration)

	b, err = httputil.DumpResponse(out, true)
	if err != nil {
		log.Printf("Failed to dump response %s %s: %v", in.Method, in.URL, err)
	}
	if b := string(b); b != "" {
		log.Println(b)
	}
	return
}
