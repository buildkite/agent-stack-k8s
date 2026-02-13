package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
)

// AgentTokenClientOpts contains the options for creating a new AgentTokenClient.
type AgentTokenClientOpts struct {
	// Token is the agent registration token.
	Token string
	// Endpoint is the Agent API endpoint. If empty, the default endpoint is used.
	Endpoint string
	// HTTPTimeout is the HTTP client timeout. If 0, DefaultHTTPTimeout is used.
	HTTPTimeout time.Duration
	// Logger is the structured logger. May be nil.
	Logger *slog.Logger
	// LogHTTPPayloads enables logging of full HTTP request/response bodies.
	LogHTTPPayloads bool
}

// AgentTokenClient is a special Agent API client: it can only be used to query
// the GET /token API.
// This client doesn't need a cluster ID or queue ID in advance.
type AgentTokenClient struct {
	endpoint   *url.URL
	httpClient *http.Client
	token      string
}

// NewAgentTokenClient creates a new AgentTokenClient.
func NewAgentTokenClient(opts AgentTokenClientOpts) (*AgentTokenClient, error) {
	if opts.Token == "" {
		return nil, errors.New("token must not be empty")
	}

	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}

	httpTimeout := opts.HTTPTimeout
	if httpTimeout == 0 {
		httpTimeout = DefaultHTTPTimeout
	}

	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "https://agent.buildkite.com/v3"
	}

	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	return &AgentTokenClient{
		endpoint: endpointURL,
		token:    opts.Token,
		httpClient: &http.Client{
			Timeout:   httpTimeout,
			Transport: NewLogTransport(http.DefaultTransport, opts.Logger, opts.LogHTTPPayloads),
		},
	}, nil
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

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating token request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.token))
	req.Header.Set("User-Agent", fmt.Sprintf("buildkite/agent-stack-k8s %s", version.Version()))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	return decodeResponse[AgentTokenIdentity](resp)
}
