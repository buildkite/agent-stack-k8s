package api

//go:generate go run github.com/Khan/genqlient

import (
	"net/http"
	"time"

	"github.com/Khan/genqlient/graphql"
)

func NewGraphQLClient(bearer, endpoint string) graphql.Client {
	if endpoint == "" {
		endpoint = "https://graphql.buildkite.com/v1"
	}
	httpClient := http.Client{
		Timeout: 60 * time.Second,
		Transport: NewLogger(&authedTransport{
			bearer:  bearer,
			wrapped: http.DefaultTransport,
		}),
	}
	return graphql.NewClient(endpoint, &httpClient)
}
