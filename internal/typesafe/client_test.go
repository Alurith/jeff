package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEvaluateSendsDocumentedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var request struct {
			State     string              `json:"state"`
			Model     string              `json:"model"`
			Questions map[string]Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.State != "package main\n// café\n" || request.Model != "jev-1.13.0" {
			t.Fatalf("request state/model = %q/%q", request.State, request.Model)
		}
		if len(request.Questions) != 1 || request.Questions["SEC001"].Type != "noul" {
			t.Fatalf("questions = %#v", request.Questions)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"SEC001":{"type":"noul","noul":0.81}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Evaluate(context.Background(), "jev-1.13.0", "package main\n// café\n", map[string]Question{
		"SEC001": {Type: "noul", Instructions: "Is this true?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Answers["SEC001"].Noul == nil || *response.Answers["SEC001"].Noul != 0.81 {
		t.Fatalf("response = %#v", response)
	}
}

func TestEvaluateDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/systemone" {
			w.Header().Set("Location", "/capture")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		redirected.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || HTTPStatus(err) != http.StatusTemporaryRedirect || redirected.Load() != 0 {
		t.Fatalf("redirected=%d status=%d error=%v", redirected.Load(), HTTPStatus(err), err)
	}
}

func TestEvaluateRejectsOversizedResponse(t *testing.T) {
	valid := []byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`)
	body := append(valid, bytes.Repeat([]byte(" "), 1<<20-len(valid))...)
	body = append(body, 'x')
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("x-typesafe-request-id", "request-large")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
	if err == nil || attempts.Load() != 1 || HTTPStatus(err) != http.StatusOK || RequestID(err) != "request-large" {
		t.Fatalf("attempts=%d request_id=%q status=%d error=%v", attempts.Load(), RequestID(err), HTTPStatus(err), err)
	}
}
