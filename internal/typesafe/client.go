package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultBaseURL = "https://api.typesafe.ai"

const (
	maxAttempts      = 3
	maxResponseBytes = 1 << 20
	attemptTimeout   = 10 * time.Second
	totalTimeout     = 30 * time.Second
)

type Client struct {
	baseURL        *url.URL
	apiKey         string
	httpClient     *http.Client
	attemptTimeout time.Duration
	totalTimeout   time.Duration
	sleep          func(context.Context, time.Duration) error
	jitter         func(time.Duration) time.Duration
}

type Question struct {
	Type         string    `json:"type"`
	Instructions string    `json:"instructions"`
	Criteria     *Criteria `json:"criteria,omitempty"`
}

type Criteria struct {
	True  string `json:"true,omitempty"`
	False string `json:"false,omitempty"`
}

type Answer struct {
	Type string   `json:"type"`
	Noul *float64 `json:"noul,omitempty"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
}

type ProviderError struct {
	StatusCode    int
	RequestID     string
	Code          string
	Err           error
	retryable     bool
	retryAfter    time.Duration
	hasRetryAfter bool
}

func (e *ProviderError) Error() string {
	message := e.Err.Error()
	if e.RequestID != "" {
		message += fmt.Sprintf(" (request %s)", e.RequestID)
	}
	return message
}

func (e *ProviderError) Unwrap() error { return e.Err }

func RequestID(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RequestID
	}
	return ""
}

func HTTPStatus(err error) int {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.StatusCode
	}
	return 0
}

func RuleCode(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Code
	}
	return ""
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid TYPESAFE_BASE_URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := &http.Client{
		Transport:     httpClient.Transport,
		Timeout:       httpClient.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{
		baseURL:        parsed,
		apiKey:         apiKey,
		httpClient:     client,
		attemptTimeout: attemptTimeout,
		totalTimeout:   totalTimeout,
		sleep:          sleepWithContext,
		jitter:         jitterDuration,
	}, nil
}

func (c *Client) Evaluate(ctx context.Context, model, state string, questions map[string]Question) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("TypeSafe client is nil")
	}
	if c.apiKey == "" {
		return Response{}, fmt.Errorf("TYPESAFE_API_KEY is required")
	}
	if model == "" || len(questions) == 0 {
		return Response{}, fmt.Errorf("model and at least one question are required")
	}
	if !utf8.ValidString(state) {
		return Response{}, fmt.Errorf("state is not valid UTF-8")
	}

	requestBody := struct {
		State     string              `json:"state"`
		Model     string              `json:"model"`
		Questions map[string]Question `json:"questions"`
	}{State: state, Model: model, Questions: questions}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return Response{}, fmt.Errorf("encode TypeSafe request: %w", err)
	}

	totalContext, cancel := context.WithTimeout(ctx, c.totalTimeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, err := c.evaluateOnce(totalContext, model, payload, questions)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt == maxAttempts-1 || !retryable(err) {
			return Response{}, err
		}
		delay := c.backoff(attempt)
		var providerErr *ProviderError
		if errors.As(err, &providerErr) && providerErr.hasRetryAfter {
			delay = providerErr.retryAfter
		}
		if err := c.sleep(totalContext, delay); err != nil {
			return Response{}, lastErr
		}
	}
	return Response{}, lastErr
}

func (c *Client) evaluateOnce(ctx context.Context, model string, payload []byte, questions map[string]Question) (Response, error) {
	attemptContext, cancel := context.WithTimeout(ctx, c.attemptTimeout)
	defer cancel()
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/systemone"
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(attemptContext, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create TypeSafe request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		if attemptContext.Err() != nil {
			err = attemptContext.Err()
		}
		return Response{}, &ProviderError{
			Err:       fmt.Errorf("TypeSafe request: %w", err),
			retryable: !errors.Is(err, context.Canceled),
		}
	}
	defer response.Body.Close()

	statusCode := response.StatusCode
	requestID := response.Header.Get("x-typesafe-request-id")
	if statusCode != http.StatusOK {
		retryAfter, hasRetryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
		return Response{}, &ProviderError{
			StatusCode:    statusCode,
			RequestID:     requestID,
			Err:           fmt.Errorf("TypeSafe returned HTTP %d", statusCode),
			retryable:     statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500,
			retryAfter:    retryAfter,
			hasRetryAfter: hasRetryAfter,
		}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, &ProviderError{
			StatusCode: statusCode,
			RequestID:  requestID,
			Err:        fmt.Errorf("read TypeSafe response: %w", err),
			retryable:  !errors.Is(err, context.Canceled),
		}
	}
	responseError := func(code string, err error) error {
		return &ProviderError{StatusCode: statusCode, RequestID: requestID, Code: code, Err: err}
	}
	if len(body) > maxResponseBytes {
		return Response{}, responseError("", fmt.Errorf("TypeSafe response exceeds %d bytes", maxResponseBytes))
	}
	var result Response
	if err := json.Unmarshal(body, &result); err != nil {
		return Response{}, responseError("", fmt.Errorf("decode TypeSafe response: %w", err))
	}
	if result.Model != model {
		return Response{}, responseError("", fmt.Errorf("TypeSafe resolved model %q, expected %q", result.Model, model))
	}
	if len(result.Answers) != len(questions) {
		return Response{}, responseError("", fmt.Errorf("TypeSafe returned %d answers for %d questions", len(result.Answers), len(questions)))
	}
	for code, question := range questions {
		answer, ok := result.Answers[code]
		if !ok {
			return Response{}, responseError(code, fmt.Errorf("TypeSafe response is missing answer %q", code))
		}
		if answer.Type != question.Type {
			return Response{}, responseError(code, fmt.Errorf("TypeSafe answer %q has type %q, expected %q", code, answer.Type, question.Type))
		}
		if question.Type == "noul" {
			if answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
				return Response{}, responseError(code, fmt.Errorf("TypeSafe answer %q has an invalid noul", code))
			}
		}
	}
	for code := range result.Answers {
		if _, ok := questions[code]; !ok {
			return Response{}, responseError(code, fmt.Errorf("TypeSafe returned unexpected answer %q", code))
		}
	}
	return result, nil
}

func retryable(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.retryable
}

func (c *Client) backoff(attempt int) time.Duration {
	base := 250 * time.Millisecond * time.Duration(1<<attempt)
	return base + c.jitter(base)
}

func jitterDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max) + 1))
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil {
		return seconds, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if delay := time.Until(when); delay > 0 {
		return delay, true
	}
	return 0, true
}
