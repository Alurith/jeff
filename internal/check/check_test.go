package check

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testRuleCount = 20

func writeNoulResponse(w http.ResponseWriter, questions map[string]json.RawMessage, noul float64, violationCode, invalidCode string) {
	answers := make(map[string]any, len(questions))
	for code := range questions {
		if code == invalidCode {
			answers[code] = map[string]string{"type": "score"}
			continue
		}
		value := noul
		if code == violationCode {
			value = 0.81
		}
		answers[code] = map[string]any{"type": "noul", "noul": value}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Model   string         `json:"model"`
		Answers map[string]any `json:"answers"`
	}{Model: "jev-1.13.0", Answers: answers})
}

func TestRunExplicitUTF8File(t *testing.T) {
	root := t.TempDir()
	content := "package main\n// café\n"
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State     string                     `json:"state"`
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.State != content || request.Model != "jev-1.13.0" || len(request.Questions) != testRuleCount {
			t.Fatalf("request = %#v", request)
		}
		writeNoulResponse(w, request.Questions, 0.10, "GEN002", "")
	}))
	defer server.Close()

	result := Run(context.Background(), Options{
		Root:    root,
		Paths:   []string{"sample.go"},
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if len(result.Errors) != 0 || len(result.Checks) != testRuleCount {
		t.Fatalf("result = %#v", result)
	}
	if result.Checks[0].Status != StatusPass || result.Checks[1].Status != StatusViolation || result.ExitCode() != 1 {
		t.Fatalf("checks = %#v, exit = %d", result.Checks, result.ExitCode())
	}
	var output strings.Builder
	if err := WriteText(&output, result); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "sample.go: GEN002 misleading-naming: Important names appear inconsistent with their behavior or purpose") {
		t.Fatalf("text output = %q", got)
	}
}

func TestRunPreservesProviderRuleCode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("x-typesafe-request-id", "request-invalid-answer")
		writeNoulResponse(w, request.Questions, 0.1, "", "GEN002")
	}))
	defer server.Close()

	result := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key", NoCache: true})
	if len(result.Errors) != 1 || result.Errors[0].Code != "GEN002" || result.Errors[0].RequestID != "request-invalid-answer" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunRejectsNULBeforeCacheAndRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n\x00secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"GEN001":{"type":"noul","noul":0.1},"GEN002":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	result := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key"})
	if len(result.Errors) != 1 || requests.Load() != 0 {
		t.Fatalf("requests=%d result=%#v", requests.Load(), result)
	}
	if !strings.Contains(result.Errors[0].Message, "NUL") {
		t.Fatalf("error = %q", result.Errors[0].Message)
	}
	if _, err := os.Stat(filepath.Join(root, ".jeff-cache")); !os.IsNotExist(err) {
		t.Fatalf("cache created for binary input: %v", err)
	}
}

func TestRunRejectsInvalidUTF8BeforeRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"GEN001":{"type":"noul","noul":0.1},"GEN002":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	result := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key"})
	if len(result.Errors) != 1 || result.ExitCode() != 2 || requests.Load() != 0 {
		t.Fatalf("requests=%d result=%#v", requests.Load(), result)
	}
	if !strings.Contains(result.Errors[0].Message, "valid UTF-8") {
		t.Fatalf("error = %q", result.Errors[0].Message)
	}
	if _, err := os.Stat(filepath.Join(root, ".jeff-cache")); !os.IsNotExist(err) {
		t.Fatalf("cache created for invalid UTF-8: %v", err)
	}
}
