package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"time"

	"jeff/internal/rules"
)

const syntheticAPIKey = "synthetic-eval-key"

type SyntheticProvider struct {
	server  *httptest.Server
	model   string
	catalog rules.Catalog
	cases   map[string]LoadedCase

	mu       sync.Mutex
	requests []SyntheticRequest
}

type SyntheticRequest struct {
	Started      time.Time
	Finished     time.Time
	InputTokens  int64
	OutputTokens int64
}

type syntheticRequest struct {
	State     string                       `json:"state"`
	Model     string                       `json:"model"`
	Questions map[string]syntheticQuestion `json:"questions"`
}

type syntheticQuestion struct {
	Type         string             `json:"type"`
	Instructions string             `json:"instructions"`
	Criteria     *syntheticCriteria `json:"criteria,omitempty"`
}

type syntheticCriteria struct {
	True  string `json:"true,omitempty"`
	False string `json:"false,omitempty"`
}

type syntheticResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]syntheticAnswer `json:"answers"`
	Usage   syntheticUsage             `json:"usage"`
}

type syntheticUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type syntheticAnswer struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
}

func NewSyntheticProvider(catalog rules.Catalog, model string, dataset Dataset) (*SyntheticProvider, error) {
	cases := make(map[string]LoadedCase, len(dataset.Cases))
	for _, item := range dataset.Cases {
		if _, exists := cases[item.SourceSHA256]; exists {
			return nil, fmt.Errorf("synthetic provider: duplicate source hash %s", item.SourceSHA256)
		}
		cases[item.SourceSHA256] = item
	}
	provider := &SyntheticProvider{model: model, catalog: catalog, cases: cases}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.handle))
	return provider, nil
}

func (p *SyntheticProvider) URL() string {
	if p == nil || p.server == nil {
		return ""
	}
	return p.server.URL
}

func (p *SyntheticProvider) Close() {
	if p != nil && p.server != nil {
		p.server.Close()
	}
}

func (p *SyntheticProvider) Snapshot() MeterSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	var inputTokens, outputTokens int64
	var latency int64
	for _, request := range p.requests {
		inputTokens += request.InputTokens
		outputTokens += request.OutputTokens
		latency += request.Finished.Sub(request.Started).Milliseconds()
	}
	return MeterSnapshot{Attempts: len(p.requests), ProviderLatencyMS: latency, UsageCount: len(p.requests), InputTokens: int64Pointer(inputTokens), OutputTokens: int64Pointer(outputTokens), Source: "synthetic"}
}

func (p *SyntheticProvider) handle(w http.ResponseWriter, request *http.Request) {
	started := time.Now()
	if request.Method != http.MethodPost || request.URL.Path != "/v1/systemone" {
		http.Error(w, "synthetic endpoint", http.StatusNotFound)
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+syntheticAPIKey {
		http.Error(w, "synthetic authorization", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		http.Error(w, "synthetic request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var input syntheticRequest
	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "synthetic invalid JSON", http.StatusBadRequest)
		return
	}
	if input.Model != p.model {
		http.Error(w, "synthetic model mismatch", http.StatusBadRequest)
		return
	}
	item, found := p.cases[hashBytes([]byte(input.State))]
	if !found {
		http.Error(w, "synthetic state is not a dataset source", http.StatusBadRequest)
		return
	}
	if string(item.SourceBytes) != input.State {
		http.Error(w, "synthetic state mismatch", http.StatusBadRequest)
		return
	}
	expected := make(map[string]syntheticQuestion)
	for _, rule := range p.catalog.Rules {
		if !rule.Applies(item.Source.Filename) {
			continue
		}
		question := syntheticQuestion{Type: rule.Question.Type, Instructions: rule.Question.Instructions}
		if rule.Question.Criteria != nil {
			question.Criteria = &syntheticCriteria{True: rule.Question.Criteria.True, False: rule.Question.Criteria.False}
		}
		expected[rule.Code] = question
	}
	if !reflect.DeepEqual(input.Questions, expected) {
		http.Error(w, "synthetic question set mismatch", http.StatusBadRequest)
		return
	}
	answers := make(map[string]syntheticAnswer, len(input.Questions))
	for code := range input.Questions {
		rule := findRule(p.catalog, code)
		if rule == nil {
			http.Error(w, "synthetic rule missing", http.StatusBadRequest)
			return
		}
		score := *rule.Decision.PassBelow / 2
		if code == item.Rule {
			score = syntheticScore(*rule, item.Label)
		}
		answers[code] = syntheticAnswer{Type: "noul", Noul: score}
	}
	finished := time.Now()
	p.mu.Lock()
	p.requests = append(p.requests, SyntheticRequest{Started: started, Finished: finished, InputTokens: 64, OutputTokens: 16})
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(syntheticResponse{Model: p.model, Answers: answers, Usage: syntheticUsage{InputTokens: 64, OutputTokens: 16}})
}

func findRule(catalog rules.Catalog, code string) *rules.Rule {
	for index := range catalog.Rules {
		if catalog.Rules[index].Code == code {
			return &catalog.Rules[index]
		}
	}
	return nil
}

func syntheticScore(rule rules.Rule, label Label) float64 {
	passBelow := *rule.Decision.PassBelow
	failAtOrAbove := *rule.Decision.FailAtOrAbove
	switch label {
	case LabelViolation:
		score := failAtOrAbove + (1-failAtOrAbove)/2
		if score < 0.9 {
			return 0.9
		}
		return score
	case LabelAmbiguous:
		return (passBelow + failAtOrAbove) / 2
	default:
		return passBelow / 2
	}
}
