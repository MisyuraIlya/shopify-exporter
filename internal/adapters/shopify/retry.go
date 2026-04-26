package shopify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"shopify-exporter/internal/adapters/shopify/dto"
)

const (
	graphqlRetryMax          = 8
	graphqlRetryBaseDelay    = 750 * time.Millisecond
	graphqlRetryMaxDelay     = 45 * time.Second
	graphqlThrottleMinDelay  = 2 * time.Second
	graphqlThrottleBuffer    = 250 * time.Millisecond
	graphqlThrottleCostFloor = 50
)

var adminGraphQLLimiter = &shopifyGraphQLLimiter{}

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

type shopifyGraphQLLimiter struct {
	requestMu sync.Mutex
	mu        sync.Mutex
	available float64
	restore   float64
	next      time.Time
}

func (l *shopifyGraphQLLimiter) begin(ctx context.Context) (func(), error) {
	l.requestMu.Lock()
	if err := l.wait(ctx); err != nil {
		l.requestMu.Unlock()
		return nil, err
	}
	return l.requestMu.Unlock, nil
}

func (l *shopifyGraphQLLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		waitUntil := l.next
		l.mu.Unlock()

		delay := time.Until(waitUntil)
		if delay <= 0 {
			return nil
		}
		if err := sleepWithContext(ctx, delay); err != nil {
			return err
		}
	}
}

func (l *shopifyGraphQLLimiter) observe(cost dto.GraphQLCost) {
	status := cost.ThrottleStatus
	if status.RestoreRate <= 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.available = status.CurrentlyAvailable
	l.restore = status.RestoreRate

	targetAvailable := cost.RequestedQueryCost
	if targetAvailable <= 0 {
		targetAvailable = graphqlThrottleCostFloor
	}
	if targetAvailable < graphqlThrottleCostFloor {
		targetAvailable = graphqlThrottleCostFloor
	}
	if status.MaximumAvailable > 0 && targetAvailable > status.MaximumAvailable {
		targetAvailable = status.MaximumAvailable
	}
	if status.CurrentlyAvailable >= targetAvailable {
		return
	}

	delay := durationUntilAvailable(status.CurrentlyAvailable, targetAvailable, status.RestoreRate)
	l.deferNextLocked(delay)
}

func (l *shopifyGraphQLLimiter) throttleDelay(cost dto.GraphQLCost, fallback time.Duration) time.Duration {
	status := cost.ThrottleStatus
	targetAvailable := cost.RequestedQueryCost
	if targetAvailable <= 0 {
		targetAvailable = graphqlThrottleCostFloor
	}

	delay := fallback
	if status.RestoreRate > 0 {
		delay = durationUntilAvailable(status.CurrentlyAvailable, targetAvailable, status.RestoreRate)
	}
	if delay < graphqlThrottleMinDelay {
		delay = graphqlThrottleMinDelay
	}

	l.mu.Lock()
	l.available = status.CurrentlyAvailable
	if status.RestoreRate > 0 {
		l.restore = status.RestoreRate
	}
	l.deferNextLocked(delay)
	l.mu.Unlock()

	return delay
}

func (l *shopifyGraphQLLimiter) deferNextLocked(delay time.Duration) {
	if delay <= 0 {
		return
	}
	next := time.Now().Add(delay)
	if next.After(l.next) {
		l.next = next
	}
}

func durationUntilAvailable(current, target, restoreRate float64) time.Duration {
	if restoreRate <= 0 || current >= target {
		return 0
	}
	seconds := (target - current) / restoreRate
	delay := time.Duration(seconds*float64(time.Second)) + graphqlThrottleBuffer
	if delay > graphqlRetryMaxDelay {
		delay = graphqlRetryMaxDelay
	}
	return delay
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
