package api

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"time"

	"github.com/Khan/genqlient/graphql"
)

func NewClient(token string) graphql.Client {
	httpClient := http.Client{
		Transport: NewLogger(&authedTransport{
			key:     token,
			wrapped: http.DefaultTransport,
		}),
	}
	return graphql.NewClient("https://graphql.buildkite.com/v1", &httpClient)
}

type authedTransport struct {
	key     string
	wrapped http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.key)
	return t.wrapped.RoundTrip(req)
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
	savedHeaders := in.Header.Clone()
	if in.Header != nil && in.Header.Get("authorization") != "" {
		in.Header.Set("authorization", "<redacted>")
	}

	b, err := httputil.DumpRequestOut(in, true)
	if err == nil {
		log.Println(string(b))
	} else {
		log.Printf("Failed to dump request %s %s: %v", in.Method, in.URL, err)
	}

	// Restore the non-redacted headers.
	in.Header = savedHeaders

	start := time.Now()
	out, err = t.inner.RoundTrip(in)
	duration := time.Since(start)
	if err != nil {
		log.Printf("<-- %v %s %s (%s)", err, in.Method, in.URL, duration)
	}
	if out != nil {
		msg := fmt.Sprintf("<-- %d", out.StatusCode)
		if out.Request != nil {
			msg = fmt.Sprintf("%s %s", msg, out.Request.URL)
		}
		msg = fmt.Sprintf("%s (%s)", msg, duration)

		log.Print(msg)

		b, err := httputil.DumpResponse(out, true)
		if err == nil {
			log.Println(string(b))
		} else {
			log.Printf("Failed to dump response %s %s: %v", in.Method, in.URL, err)
		}
	}
	return
}
