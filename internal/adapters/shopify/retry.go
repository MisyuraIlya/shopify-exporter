package shopify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"shopify-exporter/internal/adapters/shopify/dto"
)

const (
	graphqlRetryMax      = 5
	graphqlRetryBaseDelay = 500 * time.Millisecond
	graphqlRetryMaxDelay  = 10 * time.Second
)

type httpStatusError struct {
	statusCode int
	status     string
	body       string
}

func (e *httpStatusError) Error() string {
	if strings.TrimSpace(e.body) == "" {
		return fmt.Sprintf("shopify request failed: %s", e.status)
	}
	return fmt.Sprintf("shopify request failed: %s: %s", e.status, e.body)
}

func newHTTPStatusError(statusCode int, status string, body []byte) error {
	return &httpStatusError{
		statusCode: statusCode,
		status:     status,
		body:       strings.TrimSpace(string(body)),
	}
}

func isRetryableHTTPError(err error) bool {
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		switch httpErr.statusCode {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

func isThrottleGraphQLError(errs []dto.GraphQLError) bool {
	for _, e := range errs {
		if strings.Contains(strings.ToLower(e.Message), "throttled") {
			return true
		}
		if code, ok := e.Extensions["code"].(string); ok && strings.EqualFold(code, "THROTTLED") {
			return true
		}
	}
	return false
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		return 0
	}
	delay := graphqlRetryBaseDelay << attempt
	if delay > graphqlRetryMaxDelay {
		delay = graphqlRetryMaxDelay
	}
	return delay
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
