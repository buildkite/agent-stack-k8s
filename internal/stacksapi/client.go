package stacksapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/roko"
)

// Client is a Buildkite Stacks API client.
type Client struct {
	baseURL    *url.URL
	userAgent  string
	authHeader string

	logger          *slog.Logger
	logHTTPPayloads bool

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
		roko.WithStrategy(roko.ExponentialSubsecond(1 * time.Second)),
		roko.WithJitterRange(500*time.Millisecond, 1*time.Second),
	}

	defaultUserAgent = "agent-stack-k8s/" + version.Version()
)

// WithLogger sets the [*slog.Logger] for the API client. The default is a [slog.Logger] using the [slog.DiscardHandler] handler.
func WithLogger(logger *slog.Logger) ClientOpt {
	return func(c *Client) error {
		c.logger = logger
		return nil
	}
}

// LogHTTPRequestPayloads instructs the client to log all HTTP request payloads. Note that this may log sensitive information,
// and should be used with caution
func LogHTTPPayloads() ClientOpt {
	return func(c *Client) error {
		c.logHTTPPayloads = true
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

// WithHTTPClient sets the HTTP client for the API client, overriding [http.DefaultClient].
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

// NewClient creates a new API client with the given token and options. Note that the token must be a Buildkite Cluster Token,
// and that REST/GraphQL API tokens will not work.ql
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

	retrier   *roko.Retrier
	bodyBytes []byte
}

// resetBody recreates the request body from the stored bytes for retry attempts
func (r *StackAPIRequest) resetBody() {
	r.Body = io.NopCloser(bytes.NewReader(r.bodyBytes))
}

type RequestOption func(*StackAPIRequest) error

// WithRetrier sets the retrier for the request, overriding the default retrier belonging to the [Client].
func WithRetrier(retrier *roko.Retrier) RequestOption {
	return func(r *StackAPIRequest) error {
		r.retrier = retrier
		return nil
	}
}

// newRequest creates a new API request suitable for dispatch to the Buildkite Stacks API with the given context, method, path, and body.
func (c *Client) newRequest(ctx context.Context, method, path string, body any, opts ...RequestOption) (*StackAPIRequest, error) {
	fullURL := c.baseURL.JoinPath(path)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger := c.logger.With(
		"method", req.Method,
		"url", req.URL,
	)

	logger.DebugContext(ctx, "created request")

	stackReq := &StackAPIRequest{
		Request:   req,
		retrier:   roko.NewRetrier(c.retrierOpts...),
		bodyBytes: bodyBytes,
	}

	for _, opt := range opts {
		err := opt(stackReq)
		if err != nil {
			return nil, fmt.Errorf("failed to apply request option: %w", err)
		}
	}

	return stackReq, nil
}

// do performs the given *StackAPIRequest and returns the HTTP response, unmarshalling the body into val.
func (c *Client) do(ctx context.Context, req *StackAPIRequest, val any) (*http.Response, error) {
	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := c.processResponse(ctx, resp, val); err != nil {
		return nil, err
	}

	return resp, nil
}

// doWithRetry handles the retry logic for HTTP requests
func (c *Client) doWithRetry(ctx context.Context, req *StackAPIRequest) (*http.Response, error) {
	logger := c.logger.With(
		"method", req.Method,
		"url", req.URL.String(),
	)

	return roko.DoFunc(ctx, req.retrier, func(r *roko.Retrier) (*http.Response, error) {
		req.resetBody()

		sendRequestLogger := c.prepareRequestLogger(req, logger)
		sendRequestLogger.DebugContext(ctx, "sending request")

		resp, err := c.httpClient.Do(req.Request)
		if err != nil {
			return nil, err
		}

		logger := c.logger.With("response_status", resp.StatusCode)

		responseLogger := c.prepareResponseLogger(resp, logger)
		responseLogger.DebugContext(ctx, "received response")

		if err := c.handleResponseError(ctx, resp, r, logger); err != nil {
			return nil, err
		}

		return resp, nil
	})
}

// handleResponseError processes error responses and determines retry behavior
func (c *Client) handleResponseError(ctx context.Context, resp *http.Response, r *roko.Retrier, logger *slog.Logger) error {
	err := checkResponse(resp)
	if err != nil {
		var errResp *ErrorResponse
		if errors.As(err, &errResp) {
			if errResp.IsRetryableStatus() {
				logger = logger.With("retry_state", r.String())
			} else {
				r.Break()
			}

			logger.DebugContext(ctx, "request failed", "error", err)
			return err
		}

		logger.DebugContext(ctx, "request errored", "error", err)
		return err
	}
	return nil
}

// prepareRequestLogger sets up logging with optional request dump
func (c *Client) prepareRequestLogger(req *StackAPIRequest, logger *slog.Logger) *slog.Logger {
	if !c.logHTTPPayloads {
		return logger
	}

	reqDump, err := httputil.DumpRequestOut(req.Request, true)
	if err != nil {
		logger.Warn("Failed to dump request. Log won't include request body", "error", err)
		return logger
	}

	return logger.With("request", string(reqDump))
}

// processResponse handles response body reading and JSON unmarshaling
func (c *Client) processResponse(ctx context.Context, resp *http.Response, val any) error {
	if val == nil {
		return nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	defer resp.Body.Close()

	if err := json.Unmarshal(bodyBytes, val); err != nil {
		return fmt.Errorf("failed to decode response body: %w", err)
	}

	return nil
}

func (c *Client) prepareResponseLogger(resp *http.Response, logger *slog.Logger) *slog.Logger {
	if !c.logHTTPPayloads {
		return logger
	}

	respDump, err := httputil.DumpResponse(resp, true)
	if err != nil {
		logger.Warn("Failed to dump response. Log won't include response body", "error", err)
		return logger
	}

	return logger.With("response", string(respDump))
}

// ErrorResponse provides a message.
type ErrorResponse struct {
	Response *http.Response `json:"-"`       // HTTP response that caused this error
	RawBody  []byte         `json:"-"`       // Raw Response Body
	Message  string         `json:"message"` // Error message from Buildkite API
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
		return fmt.Errorf("response failed with error %w, but parsing response body JSON failed with error: %w. Raw body of error was: %q", errorResponse, err, string(data))
	}
	return errorResponse
}
