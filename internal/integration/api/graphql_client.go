package api

//go:generate go run github.com/Khan/genqlient

import (
	"net/http"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"

	"github.com/Khan/genqlient/graphql"
)

func NewGraphQLClient(bearer, endpoint string) graphql.Client {
	if endpoint == "" {
		endpoint = "https://graphql.buildkite.com/v1"
	}
	httpClient := http.Client{
		Timeout:   60 * time.Second,
		Transport: api.NewLogger(api.NewAuthedTransportWithBearer(http.DefaultTransport, bearer)),
	}
	return graphql.NewClient(endpoint, &httpClient)
}
