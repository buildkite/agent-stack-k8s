package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// This is a special agent client: it can only be used to query the GET /token API.
// Meaning for this client to function, there isn't a need for a cluster id nor queue id.
func NewAgentTokenClient(token, endpoint string) (*AgentTokenClient, error) {
	if endpoint == "" {
		endpoint = "https://agent.buildkite.com/v3"
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return &AgentTokenClient{
		endpoint: endpointURL,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: NewLogger(NewAuthedTransportWithToken(http.DefaultTransport, token)),
		},
	}, nil
}

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

// GetJobState gets the state of a specific job.
func (c *AgentTokenClient) GetTokenIdentity(ctx context.Context) (result *AgentTokenIdentity, retryAfter time.Duration, err error) {
	u := c.endpoint.JoinPath("token")

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	return decodeResponse[AgentTokenIdentity](resp)
}
