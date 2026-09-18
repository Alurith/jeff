package check

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRunTreatsContextLimitAsTerminal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Questions) == 2 {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"context limit exceeded"}`))
			return
		}
		code := "GEN001"
		if _, ok := request.Questions[code]; !ok {
			code = "SEC001"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"` + code + `":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	result := Run(context.Background(), Options{Root: root, Paths: []string{"sample.go"}, BaseURL: server.URL, APIKey: "test-key"})
	if len(result.Errors) != 1 || len(result.Checks) != 0 || requests.Load() != 1 {
		t.Fatalf("requests=%d result=%#v", requests.Load(), result)
	}
	if _, err := os.Stat(filepath.Join(root, ".jeff-cache")); !os.IsNotExist(err) {
		t.Fatalf("cache created after failed request: %v", err)
	}
}
