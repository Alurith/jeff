package typesafe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEvaluateRetriesRateLimitAndHonorsRequestIDOnFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.Header().Set("x-typesafe-request-id", "request-ignored")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	client.jitter = func(time.Duration) time.Duration { return 0 }
	if _, err := client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}}); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestEvaluateUsesConfiguredTimeoutBudgets(t *testing.T) {
	var attempts, sleeps atomic.Int32
	client, err := NewClient("https://example.invalid", "test-key", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts.Add(1)
		deadline, ok := request.Context().Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 59*time.Minute || remaining > time.Hour {
			t.Errorf("attempt deadline remaining = %s, ok=%v", remaining, ok)
		}
		return nil, io.ErrUnexpectedEOF
	})})
	if err != nil {
		t.Fatal(err)
	}
	if client.attemptTimeout != attemptTimeout || client.totalTimeout != totalTimeout {
		t.Fatalf("default budgets = %s/%s", client.attemptTimeout, client.totalTimeout)
	}
	client.attemptTimeout = time.Hour
	client.totalTimeout = 2 * time.Hour
	client.sleep = func(ctx context.Context, _ time.Duration) error {
		sleeps.Add(1)
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 119*time.Minute || remaining > 2*time.Hour {
			t.Errorf("total deadline remaining = %s, ok=%v", remaining, ok)
		}
		return nil
	}
	client.jitter = func(time.Duration) time.Duration { return 0 }
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 3 || sleeps.Load() != 2 {
		t.Fatalf("attempts=%d sleeps=%d error=%v", attempts.Load(), sleeps.Load(), err)
	}
}

func TestEvaluateRetriesTransportErrors(t *testing.T) {
	var attempts atomic.Int32
	client, err := NewClient("https://example.invalid", "test-key", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, io.ErrUnexpectedEOF
	})})
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	client.jitter = func(time.Duration) time.Duration { return 0 }
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 3 {
		t.Fatalf("attempts=%d error=%v", attempts.Load(), err)
	}
}

func TestEvaluateRetriesTruncatedResponsesAndKeepsLastRequestID(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("Content-Length", "100")
		w.Header().Set("x-typesafe-request-id", fmt.Sprintf("request-%d", attempt))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	client.jitter = func(time.Duration) time.Duration { return 0 }
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 3 || RequestID(err) != "request-3" || HTTPStatus(err) != http.StatusOK {
		t.Fatalf("attempts=%d request_id=%q status=%d error=%v", attempts.Load(), RequestID(err), HTTPStatus(err), err)
	}
}

func TestEvaluateDoesNotRetryMalformedResponseAndKeepsMetadata(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("x-typesafe-request-id", "request-200")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 1 || RequestID(err) != "request-200" || HTTPStatus(err) != http.StatusOK {
		t.Fatalf("attempts=%d request_id=%q status=%d error=%v", attempts.Load(), RequestID(err), HTTPStatus(err), err)
	}
}

func TestEvaluateDoesNotRetryTruncatedClientError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "100")
		w.Header().Set("x-typesafe-request-id", "request-422")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 1 || HTTPStatus(err) != http.StatusUnprocessableEntity || RequestID(err) != "request-422" {
		t.Fatalf("attempts=%d request_id=%q status=%d error=%v", attempts.Load(), RequestID(err), HTTPStatus(err), err)
	}
}

func TestEvaluateRetriesHTTPStatusClasses(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) < 3 {
					w.WriteHeader(status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "test-key", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			client.sleep = func(context.Context, time.Duration) error { return nil }
			client.jitter = func(time.Duration) time.Duration { return 0 }
			if _, err := client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}}); err != nil {
				t.Fatal(err)
			}
			if attempts.Load() != 3 {
				t.Fatalf("attempts = %d", attempts.Load())
			}
		})
	}
}

func TestEvaluateDoesNotRetryGenericClientError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("x-typesafe-request-id", "request-123")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"malformed question"}`))
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "test-key", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
			if err == nil || !strings.Contains(err.Error(), "request-123") {
				t.Fatalf("error = %v", err)
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
