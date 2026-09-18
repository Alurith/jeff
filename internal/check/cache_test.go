package check

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRunUsesPerAnswerCacheBeforeCredentials(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sample.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Questions) != 2 {
			t.Fatalf("questions = %#v", request.Questions)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"GEN001":{"type":"noul","noul":0.1},"SEC001":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	first := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key"})
	if len(first.Errors) != 0 || requests.Load() != 1 {
		t.Fatalf("first run requests=%d result=%#v", requests.Load(), first)
	}
	second := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL})
	if len(second.Errors) != 0 || requests.Load() != 1 {
		t.Fatalf("cached run requests=%d result=%#v", requests.Load(), second)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".jeff-cache", "v1"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("cache entries=%v err=%v", entries, err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, ".jeff-cache", "v1", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("package main")) {
			t.Fatal("cache entry persisted source content")
		}
	}
	third := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key", NoCache: true})
	if len(third.Errors) != 0 || requests.Load() != 2 {
		t.Fatalf("no-cache run requests=%d result=%#v", requests.Load(), third)
	}
}
