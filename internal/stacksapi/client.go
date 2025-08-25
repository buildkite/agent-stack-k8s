package stacksapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/roko"
)

type Client struct {
	baseURL    *url.URL
	userAgent  string
	authHeader string

	logger      *slog.Logger
	httpClient  *http.Client
	retrierOpts []roko.RetrierOpt
}

func urlMustParse(u string) *url.URL {
	parsed, err := url.Parse(u)
	if err != nil {
		panic(err)
	}
	return parsed
}

type ClientOpt func(*Client) error

const (
	DefaultBaseURL = "https://agent.buildkite.com/v3/"
	DefaultQueue   = "_default"
)

var (
	DefaultRetrierOptions = []roko.RetrierOpt{
		roko.WithMaxAttempts(5),
		roko.WithStrategy(roko.Constant(1 * time.Second)),
		roko.WithJitterRange(500*time.Millisecond, 1*time.Second),
	}

	defaultUserAgent = "agent-stack-k8s/internal/stacksapi/" + version.Version()
)

func WithLogger(logger *slog.Logger) ClientOpt {
	return func(c *Client) error {
		c.logger = logger
		return nil
	}
}

// WithBaseURL sets the base URL for the API client, overriding [DefaultBaseURL]
func WithBaseURL(baseURL *url.URL) ClientOpt {
	return func(c *Client) error {
		c.baseURL = baseURL
		return nil
	}
}

// PrependToUserAgent adds a prefix to the User-Agent header for the API client. Note that overriding of the user-agent
// is not supported (though can probably be worked around).
func PrependToUserAgent(prefix string) ClientOpt {
	return func(c *Client) error {
		c.userAgent = prefix + " " + c.userAgent
		return nil
	}
}

// WithHTTPClient sets the HTTP client for the API client.
func WithHTTPClient(httpClient *http.Client) ClientOpt {
	return func(c *Client) error {
		c.httpClient = httpClient
		return nil
	}
}

// WithRetrierOptions sets the default retrier options for the API client. These can be overridden per-request by using the [WithRetrier] RequestOpt.
func WithRetrierOptions(opts ...roko.RetrierOpt) ClientOpt {
	return func(c *Client) error {
		c.retrierOpts = opts
		return nil
	}
}

// NewClient creates a new API client with the given API token and options.
func NewClient(apiToken string, opts ...ClientOpt) (*Client, error) {
	client := &Client{
		httpClient:  http.DefaultClient,
		baseURL:     urlMustParse(DefaultBaseURL),
		userAgent:   defaultUserAgent,
		logger:      slog.New(slog.DiscardHandler),
		authHeader:  fmt.Sprintf("Token %s", apiToken),
		retrierOpts: DefaultRetrierOptions,
	}

	for _, opt := range opts {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	return client, nil
}

type StackAPIRequest struct {
	*http.Request

	retrier  *roko.Retrier
	bodyData []byte
}

type RequestOption func(*StackAPIRequest) error

func WithRetrier(retrier *roko.Retrier) RequestOption {
	return func(r *StackAPIRequest) error {
		r.retrier = retrier
		return nil
	}
}

// NewRequest creates a new API request suitable for dispatch to the Buildkite Stacks API with the given context, method, path, and body.
func (c *Client) NewRequest(ctx context.Context, method, path string, body any, opts ...RequestOption) (*StackAPIRequest, error) {
	fullURL := c.baseURL.ResolveReference(&url.URL{Path: path})

	var bodyData []byte
	if body != nil {
		var err error
		bodyData, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bytes.NewReader(bodyData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Content-Type", "application/json")

	c.logger.With(
		"method", req.Method,
		"url", req.URL,
		"request_headers", req.Header,
		"request_body", string(bodyData),
	).DebugContext(ctx, "created request")

	stackReq := &StackAPIRequest{
		Request:  req,
		retrier:  roko.NewRetrier(c.retrierOpts...),
		bodyData: bodyData,
	}

	for _, opt := range opts {
		err := opt(stackReq)
		if err != nil {
			return nil, fmt.Errorf("failed to apply request option: %w", err)
		}
	}

	return stackReq, nil
}

// Do performs the given *StackAPIRequest and returns the HTTP response, unmarshalling the body of the HTTP response into
// val, given it's a pointer to the expected response type.
//
// The Do function will handle retries automatically based on the retrier options set in the request.
func (c *Client) Do(ctx context.Context, req *StackAPIRequest, val any) (*http.Response, error) {
	c.logger.With(
		"method", req.Method,
		"url", req.URL.String(),
	).DebugContext(ctx, "sending request")

	resp, err := roko.DoFunc(ctx, req.retrier, func(r *roko.Retrier) (*http.Response, error) {
		// Create a fresh body reader for each retry attempt
		if len(req.bodyData) > 0 {
			req.Body = io.NopCloser(bytes.NewReader(req.bodyData))
		}

		resp, err := c.httpClient.Do(req.Request)
		if err != nil {
			return nil, err
		}

		err = checkResponse(resp)
		if err != nil {
			errResp := err.(*ErrorResponse)
			if !errResp.IsRetryableStatus() {
				r.Break()
			}
			return nil, err
		}

		return resp, nil
	})
	if err != nil {
		c.logger.ErrorContext(ctx, "request failed", "error", err)
		return nil, err
	}

	if val != nil {
		if err := json.NewDecoder(resp.Body).Decode(val); err != nil {
			c.logger.ErrorContext(ctx, "failed to decode response body", "error", err)
			return nil, err
		}
	}

	return resp, nil
}

// ErrorResponse provides a message.
type ErrorResponse struct {
	Response *http.Response // HTTP response that caused this error
	Message  string         `json:"message"` // Error message from Buildkite API
	RawBody  []byte         `json:"-"`       // Raw Response Body
}

func (r *ErrorResponse) Error() string {
	return fmt.Sprintf("%v %v: %d %v",
		r.Response.Request.Method, r.Response.Request.URL,
		r.Response.StatusCode, r.Message)
}

func (r *ErrorResponse) IsRetryableStatus() bool {
	switch {
	case r.Response.StatusCode >= 500:
		return true
	case r.Response.StatusCode == http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func checkResponse(r *http.Response) error {
	if c := r.StatusCode; 200 <= c && c <= 299 {
		return nil
	}

	errorResponse := &ErrorResponse{Response: r}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("response failed with error %w, but reading response body failed with error %w", errorResponse, err)
	}
	errorResponse.RawBody = data

	err = json.Unmarshal(data, errorResponse)
	if err != nil {
		return fmt.Errorf("response failed with error %w, but parsing response body JSON failed with error: %w. Raw body of error was: %s", errorResponse, err, string(data))
	}
	return errorResponse
}
