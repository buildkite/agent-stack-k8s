package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// NewAgentTokenClient creates a new AgentTokenClient.
// If httpTimeout is 0, DefaultHTTPTimeout is used.
func NewAgentTokenClient(token, endpoint string, httpTimeout time.Duration) (*AgentTokenClient, error) {
	if endpoint == "" {
		endpoint = "https://agent.buildkite.com/v3"
	}
	if httpTimeout == 0 {
		httpTimeout = DefaultHTTPTimeout
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return &AgentTokenClient{
		endpoint: endpointURL,
		httpClient: &http.Client{
			Timeout:   httpTimeout,
			Transport: NewLogger(NewAuthedTransportWithToken(http.DefaultTransport, token)),
		},
	}, nil
}

// AgentTokenClient is a special Agent API client: it can only be used to query
// the GET /token API.
// This client doesn't need a cluster ID or queue ID in advance.
type AgentTokenClient struct {
	endpoint   *url.URL
	httpClient *http.Client
}

// AgentTokenIdentity describes the token identity information.
type AgentTokenIdentity struct {
	UUID                  string `json:"uuid"`
	Description           string `json:"description"`
	TokenType             string `json:"token_type"`
	OrganizationSlug      string `json:"organization_slug"`
	OrganizationUUID      string `json:"organization_uuid"`
	ClusterUUID           string `json:"cluster_uuid"`
	ClusterName           string `json:"cluster_name"`
	OrganizationQueueUUID string `json:"organization_queue_uuid"`
	OrganizationQueueKey  string `json:"organization_queue_key"`
}

// GetTokenIdentity gets the identity information of an agent token.
func (c *AgentTokenClient) GetTokenIdentity(ctx context.Context) (result *AgentTokenIdentity, retryAfter time.Duration, err error) {
	u := c.endpoint.JoinPath("token")

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	return decodeResponse[AgentTokenIdentity](resp)
}
