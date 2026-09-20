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
		if len(request.Questions) != testRuleCount {
			t.Fatalf("questions = %#v", request.Questions)
		}
		writeNoulResponse(w, request.Questions, 0.1, "", "")
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
	if err != nil {
		t.Fatalf("cache entries=%v err=%v", entries, err)
	}
	jsonEntries := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		jsonEntries++
		data, err := os.ReadFile(filepath.Join(root, ".jeff-cache", "v1", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("package main")) {
			t.Fatal("cache entry persisted source content")
		}
	}
	if jsonEntries != testRuleCount {
		t.Fatalf("cache JSON entries=%d, want %d", jsonEntries, testRuleCount)
	}
	if data, err := os.ReadFile(filepath.Join(root, ".jeff-cache", "v1", ".gitignore")); err != nil || string(data) != "*\n" {
		t.Fatalf("cache gitignore = %q, error = %v", data, err)
	}
	third := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key", NoCache: true})
	if len(third.Errors) != 0 || requests.Load() != 2 {
		t.Fatalf("no-cache run requests=%d result=%#v", requests.Load(), third)
	}
}
