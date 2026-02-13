package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/google/uuid"
)

type logTransport struct {
	inner       http.RoundTripper
	logPayloads bool
	logger      *slog.Logger
}

// NewLogger wraps inner with HTTP payload logging. Logging is enabled if the
// enabled argument true..
func NewLogTransport(inner http.RoundTripper, logger *slog.Logger, logPayloads bool) http.RoundTripper {
	return &logTransport{inner: inner, logPayloads: logPayloads, logger: logger}
}

func (t *logTransport) RoundTrip(in *http.Request) (*http.Response, error) {
	logger := t.logger.With(
		"method", in.Method,
		"url", in.URL.String(),
		"request_id", uuid.New().String(),
	)

	// Buffer the body so that dumping the request doesn't drain it.
	if t.logPayloads && in.Body != nil {
		body, err := io.ReadAll(in.Body)
		if err != nil {
			logger.Warn("Failed to read request body", "error", err)
		}
		in.Body.Close()
		in.Body = io.NopCloser(bytes.NewReader(body))
		in.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	// Save these headers so we can redact Authorization.
	inCopy := in
	if in.Header != nil && in.Header.Get("authorization") != "" {
		inCopy = in.Clone(in.Context())
		inCopy.Header.Set("authorization", "<redacted>")
	}

	reqLogger := logger
	if t.logPayloads {
		b, err := httputil.DumpRequestOut(inCopy, true)
		if err != nil {
			logger.Warn("Failed to dump request", "error", err)
		}

		reqLogger = reqLogger.With("request_body", string(b))
	}

	reqLogger.Debug("making request")

	start := time.Now()
	out, err := t.inner.RoundTrip(in)
	duration := time.Since(start)
	if err != nil {
		logger.With(
			"error", err,
			"duration", duration,
		).Debug("response error")
		return out, err
	}

	if out == nil {
		return out, err
	}

	logger = logger.With(
		"response_code", out.StatusCode,
		"duration", duration,
	)

	if t.logPayloads {
		b, err := httputil.DumpResponse(out, true)
		if err != nil {
			logger.Warn("Failed to dump response", "error", err)
		}

		logger = logger.With("response_body", string(b))
	}

	logger.Debug("response received")

	return out, err
}
