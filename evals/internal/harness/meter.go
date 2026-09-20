package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

type MeterSnapshot struct {
	Attempts          int
	ProviderLatencyMS int64
	UsageCount        int
	UnknownUsageCount int
	InputTokens       *int64
	OutputTokens      *int64
	ReportedCostCount int
	ReportedCostUSD   *float64
	Source            string
}

type providerMeter interface {
	URL() string
	Snapshot() MeterSnapshot
	Close()
}

type MeterProxy struct {
	server        *httptest.Server
	upstream      *url.URL
	client        *http.Client
	requestLimit  int64
	responseLimit int64

	mu       sync.Mutex
	snapshot MeterSnapshot
}

type providerUsage struct {
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	CostUSD      *float64 `json:"cost"`
}

type providerEnvelope struct {
	Usage *providerUsage `json:"usage,omitempty"`
}

func validateRealBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("real provider requires an HTTPS TYPESAFE_BASE_URL")
	}
	return nil
}

func NewMeterProxy(baseURL string, requestLimit, responseLimit int64) (*MeterProxy, error) {
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai"
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid TYPESAFE_BASE_URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if requestLimit <= 0 || responseLimit <= 0 {
		return nil, fmt.Errorf("meter limits must be positive")
	}
	proxy := &MeterProxy{
		upstream:      parsed,
		requestLimit:  requestLimit,
		responseLimit: responseLimit,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	proxy.server = httptest.NewServer(http.HandlerFunc(proxy.handle))
	return proxy, nil
}

func (p *MeterProxy) URL() string {
	if p == nil || p.server == nil {
		return ""
	}
	return p.server.URL
}

func (p *MeterProxy) Close() {
	if p != nil && p.server != nil {
		p.server.Close()
	}
}

func (p *MeterProxy) Snapshot() MeterSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneMeterSnapshot(p.snapshot)
}

func (p *MeterProxy) handle(w http.ResponseWriter, request *http.Request) {
	started := time.Now()
	if request.Method != http.MethodPost || request.URL.Path != "/v1/systemone" {
		http.Error(w, "meter proxy endpoint", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, p.requestLimit+1))
	if err != nil {
		p.record(MeterSnapshot{Attempts: 1, ProviderLatencyMS: time.Since(started).Milliseconds(), Source: "provider"})
		http.Error(w, "meter proxy request", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > p.requestLimit {
		p.record(MeterSnapshot{Attempts: 1, ProviderLatencyMS: time.Since(started).Milliseconds(), UnknownUsageCount: 1, Source: "provider"})
		http.Error(w, "meter proxy request too large", http.StatusRequestEntityTooLarge)
		return
	}
	endpoint := *p.upstream
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/systemone"
	endpoint.RawPath = ""
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		p.record(MeterSnapshot{Attempts: 1, ProviderLatencyMS: time.Since(started).Milliseconds(), UnknownUsageCount: 1, Source: "provider"})
		http.Error(w, "meter proxy request", http.StatusBadGateway)
		return
	}
	for key, values := range request.Header {
		for _, value := range values {
			upstreamRequest.Header.Add(key, value)
		}
	}
	response, err := p.client.Do(upstreamRequest)
	if err != nil {
		p.record(MeterSnapshot{Attempts: 1, ProviderLatencyMS: time.Since(started).Milliseconds(), UnknownUsageCount: 1, Source: "provider"})
		http.Error(w, "meter proxy upstream", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, p.responseLimit+1))
	latency := time.Since(started).Milliseconds()
	if readErr != nil || int64(len(responseBody)) > p.responseLimit {
		p.record(MeterSnapshot{Attempts: 1, ProviderLatencyMS: latency, UnknownUsageCount: 1, Source: "provider"})
		http.Error(w, "meter proxy response too large", http.StatusBadGateway)
		return
	}
	measurement := MeterSnapshot{Attempts: 1, ProviderLatencyMS: latency, Source: "provider"}
	var envelope providerEnvelope
	if json.Unmarshal(responseBody, &envelope) == nil && envelope.Usage != nil {
		if envelope.Usage.InputTokens != nil && envelope.Usage.OutputTokens != nil && *envelope.Usage.InputTokens >= 0 && *envelope.Usage.OutputTokens >= 0 {
			measurement.UsageCount = 1
			measurement.InputTokens = cloneInt64(envelope.Usage.InputTokens)
			measurement.OutputTokens = cloneInt64(envelope.Usage.OutputTokens)
		} else {
			measurement.UnknownUsageCount = 1
		}
		if envelope.Usage.CostUSD != nil && *envelope.Usage.CostUSD >= 0 && !isInvalidFloat(*envelope.Usage.CostUSD) {
			measurement.ReportedCostCount = 1
			measurement.ReportedCostUSD = cloneFloat(envelope.Usage.CostUSD)
		}
	} else {
		measurement.UnknownUsageCount = 1
	}
	p.record(measurement)
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func (p *MeterProxy) record(measurement MeterSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot.Attempts += measurement.Attempts
	p.snapshot.ProviderLatencyMS += measurement.ProviderLatencyMS
	p.snapshot.UsageCount += measurement.UsageCount
	p.snapshot.UnknownUsageCount += measurement.UnknownUsageCount
	p.snapshot.ReportedCostCount += measurement.ReportedCostCount
	p.snapshot.InputTokens = addInt64(p.snapshot.InputTokens, measurement.InputTokens)
	p.snapshot.OutputTokens = addInt64(p.snapshot.OutputTokens, measurement.OutputTokens)
	p.snapshot.ReportedCostUSD = addFloat(p.snapshot.ReportedCostUSD, measurement.ReportedCostUSD)
	if measurement.Source != "" {
		p.snapshot.Source = measurement.Source
	}
}

func meterDelta(before, after MeterSnapshot) MeterSnapshot {
	return MeterSnapshot{
		Attempts:          after.Attempts - before.Attempts,
		ProviderLatencyMS: after.ProviderLatencyMS - before.ProviderLatencyMS,
		UsageCount:        after.UsageCount - before.UsageCount,
		UnknownUsageCount: after.UnknownUsageCount - before.UnknownUsageCount,
		InputTokens:       subtractInt64(after.InputTokens, before.InputTokens),
		OutputTokens:      subtractInt64(after.OutputTokens, before.OutputTokens),
		ReportedCostCount: after.ReportedCostCount - before.ReportedCostCount,
		ReportedCostUSD:   subtractFloat(after.ReportedCostUSD, before.ReportedCostUSD),
		Source:            after.Source,
	}
}

func applyMetering(result *CaseResult, measurement MeterSnapshot, pricing Pricing) {
	result.Attempts = measurement.Attempts
	if measurement.Attempts > 0 {
		result.ProviderLatencyMS = measurement.ProviderLatencyMS
	} else {
		result.ProviderLatencyMS = -1
	}
	result.UsageSource = measurement.Source
	if measurement.Attempts == 0 {
		result.CostStatus = "no_request"
		return
	}
	if measurement.UnknownUsageCount == 0 && measurement.UsageCount == measurement.Attempts && measurement.InputTokens != nil && measurement.OutputTokens != nil {
		result.InputTokens = cloneInt64(measurement.InputTokens)
		result.OutputTokens = cloneInt64(measurement.OutputTokens)
	} else {
		result.UsageSource = "unknown"
	}
	if measurement.ReportedCostCount > 0 {
		result.ProviderCostUSD = cloneFloat(measurement.ReportedCostUSD)
	}
	if result.InputTokens != nil && result.OutputTokens != nil {
		if pricing.InputUSDPerMillion != nil && pricing.OutputUSDPerMillion != nil {
			calculated := float64(*result.InputTokens)*(*pricing.InputUSDPerMillion)/1_000_000 + float64(*result.OutputTokens)*(*pricing.OutputUSDPerMillion)/1_000_000
			result.CalculatedCostUSD = &calculated
			result.CostUSD = cloneFloat(&calculated)
			if result.ProviderCostUSD != nil {
				result.CostStatus = "calculated_and_reported"
			} else if result.UsageSource == "synthetic" {
				result.CostStatus = "synthetic_calculated"
			} else {
				result.CostStatus = "calculated"
			}
			return
		}
		result.CostStatus = "unknown_pricing"
		return
	}
	if result.ProviderCostUSD != nil {
		result.CostStatus = "unknown_usage_reported_cost"
	} else {
		result.CostStatus = "unknown_usage"
	}
}

func cloneMeterSnapshot(value MeterSnapshot) MeterSnapshot {
	value.InputTokens = cloneInt64(value.InputTokens)
	value.OutputTokens = cloneInt64(value.OutputTokens)
	value.ReportedCostUSD = cloneFloat(value.ReportedCostUSD)
	return value
}

func int64Pointer(value int64) *int64 { return &value }

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func addInt64(left, right *int64) *int64 {
	if right == nil {
		return cloneInt64(left)
	}
	if left == nil {
		return cloneInt64(right)
	}
	value := *left + *right
	return &value
}

func subtractInt64(left, right *int64) *int64 {
	if left == nil {
		return nil
	}
	if right == nil {
		return cloneInt64(left)
	}
	value := *left - *right
	return &value
}

func addFloat(left, right *float64) *float64 {
	if right == nil {
		return cloneFloat(left)
	}
	if left == nil {
		return cloneFloat(right)
	}
	value := *left + *right
	return &value
}

func subtractFloat(left, right *float64) *float64 {
	if left == nil {
		return nil
	}
	if right == nil {
		return cloneFloat(left)
	}
	value := *left - *right
	return &value
}
