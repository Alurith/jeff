package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"jeff/internal/rules"
	"jeff/internal/typesafe"
)

func TestRealProviderRequiresHTTPSUpstream(t *testing.T) {
	if err := validateRealBaseURL("http://localhost:8080"); err == nil {
		t.Fatal("HTTP upstream unexpectedly accepted")
	}
	if err := validateRealBaseURL("https://api.typesafe.ai"); err != nil {
		t.Fatal(err)
	}
}

func TestMeterProxyForwardsAndRecordsUsage(t *testing.T) {
	var body []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Fatalf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization was not forwarded")
		}
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("x-typesafe-request-id", "request-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"GEN001":{"type":"noul","noul":0.1}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.01}}`)
	}))
	defer upstream.Close()
	proxy, err := NewMeterProxy(upstream.URL, 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	request, err := http.NewRequest(http.MethodPost, proxy.URL()+"/v1/systemone", strings.NewReader(`{"state":"package x","model":"jev-1.13.0","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("x-typesafe-request-id") != "request-1" {
		t.Fatalf("response = %d headers=%v", response.StatusCode, response.Header)
	}
	if string(responseBody) == "" || string(body) == "" {
		t.Fatal("proxy did not forward body")
	}
	snapshot := proxy.Snapshot()
	if snapshot.Attempts != 1 || snapshot.UsageCount != 1 || snapshot.UnknownUsageCount != 0 || *snapshot.InputTokens != 10 || *snapshot.OutputTokens != 2 || *snapshot.ReportedCostUSD != 0.01 || snapshot.Source != "provider" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestMeterProxyDoesNotRetryButRecordsClientRetries(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":4,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	proxy, err := NewMeterProxy(upstream.URL, 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client, err := typesafe.NewClient(proxy.URL(), "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Evaluate(context.Background(), "jev-1.13.0", "package x", map[string]typesafe.Question{"q": {Type: "noul"}})
	if err != nil || response.Model != "jev-1.13.0" {
		t.Fatalf("response=%#v error=%v", response, err)
	}
	snapshot := proxy.Snapshot()
	if calls.Load() != 3 || snapshot.Attempts != 3 || snapshot.UsageCount != 1 || snapshot.UnknownUsageCount != 2 {
		t.Fatalf("calls=%d snapshot=%#v", calls.Load(), snapshot)
	}
}

func TestMeteringCostIsExplicitWhenUnknown(t *testing.T) {
	input, output := 10.0, 2.0
	pricing := Pricing{InputUSDPerMillion: &input, OutputUSDPerMillion: &output}
	result := CaseResult{}
	applyMetering(&result, MeterSnapshot{Attempts: 1, UsageCount: 1, InputTokens: int64Pointer(100), OutputTokens: int64Pointer(50), Source: "provider"}, pricing)
	if result.CostUSD == nil || result.CostStatus != "calculated" || *result.CostUSD != 0.0011 {
		t.Fatalf("calculated result = %#v", result)
	}
	result = CaseResult{}
	applyMetering(&result, MeterSnapshot{Attempts: 1, UnknownUsageCount: 1, Source: "provider"}, pricing)
	if result.CostUSD != nil || result.CostStatus != "unknown_usage" {
		t.Fatalf("unknown result = %#v", result)
	}
	result = CaseResult{}
	applyMetering(&result, MeterSnapshot{Attempts: 1, UsageCount: 1, InputTokens: int64Pointer(100), OutputTokens: int64Pointer(50), Source: "provider"}, Pricing{})
	if result.CostUSD != nil || result.CostStatus != "unknown_pricing" {
		t.Fatalf("missing pricing result = %#v", result)
	}
}

func TestCostMetricsRemainNullForPartialUsage(t *testing.T) {
	input := int64(100)
	metrics, err := ComputeMetrics([]CaseResult{{CaseID: "a", Rule: "GEN001", Label: LabelClean, EvalStatus: "pass", TargetNoul: floatPtr(0.1), ProcessValid: true, Attempts: 1, InputTokens: &input, OutputTokens: int64Pointer(5), CostUSD: floatPtr(0.001), CalculatedCostUSD: floatPtr(0.001), CostStatus: "calculated"}, {CaseID: "b", Rule: "GEN001", Label: LabelViolation, EvalStatus: "violation", TargetNoul: floatPtr(0.9), ProcessValid: true, Attempts: 1, CostStatus: "unknown_usage"}}, testCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Cost.CostUSD != nil || metrics.Cost.Status != "unknown_usage" || metrics.Cost.UsageObservations != 1 {
		t.Fatalf("cost metrics = %#v", metrics.Cost)
	}
}

func testCatalog(t *testing.T) rules.Catalog {
	t.Helper()
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
