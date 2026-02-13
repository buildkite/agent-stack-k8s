package api

//go:generate go run github.com/Khan/genqlient

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/lmittmann/tint"

	"github.com/Khan/genqlient/graphql"
)

func NewGraphQLClient(bearer, endpoint string) graphql.Client {
	if endpoint == "" {
		endpoint = "https://graphql.buildkite.com/v1"
	}
	logger := slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level: slog.LevelDebug,
	}))
	logRequests := false // Change to true if you want request payloads logged in tests
	httpClient := http.Client{
		Timeout:   60 * time.Second,
		Transport: api.NewLogTransport(newAuthedTransportWithBearer(http.DefaultTransport, bearer), logger, logRequests),
	}
	return graphql.NewClient(endpoint, &httpClient)
}

func newAuthedTransportWithBearer(inner http.RoundTripper, bearer string) http.RoundTripper {
	return &authedTransport{wrapped: inner, bearer: bearer}
}

type authedTransport struct {
	bearer  string
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

	reqCopy.Header.Set("Authorization", "Bearer "+t.bearer)
	reqCopy.Header.Set("User-Agent", fmt.Sprintf("buildkite/agent-stack-k8s %s", version.Version()))

	reqBodyClosed = true
	return t.wrapped.RoundTrip(reqCopy)
}
