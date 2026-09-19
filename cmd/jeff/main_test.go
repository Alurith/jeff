package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"jeff/internal/credentials"
)

const cliRuleCount = 20

type jsonResult struct {
	Checks []struct {
		Status string `json:"status"`
	} `json:"checks"`
	Errors []struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		HTTPStatus int    `json:"http_status"`
		RequestID  string `json:"request_id"`
	} `json:"errors"`
}

func TestRunRecognizesAuthCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"auth", "--help"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout.String(), "auth login") || !strings.Contains(stdout.String(), "auth logout") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunAuthLoginAndLogout(t *testing.T) {
	keyring.MockInit()
	const secret = "synthetic-secret"
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"auth", "login"}, strings.NewReader(secret+"\r\n"), &stdout, &stderr); exit != 0 {
		t.Fatalf("login exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("API key appeared in command output")
	}
	stored, err := keyring.Get("jeff", "typesafe-api-key")
	if err != nil || stored != secret {
		t.Fatalf("stored key mismatch: err=%v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if exit := run([]string{"auth", "logout"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("logout exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if _, err := keyring.Get("jeff", "typesafe-api-key"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("keyring entry remains: %v", err)
	}
	if exit := run([]string{"auth", "logout"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("idempotent logout exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestRunAuthDoesNotPrintSecretOnBackendFailure(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	keyring.MockInitWithError(backendErr)
	defer keyring.MockInit()
	const secret = "synthetic-secret"
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"auth", "login"}, strings.NewReader(secret+"\n"), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("API key appeared in failed command output")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), backendErr.Error()) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestReadAPIKeyBoundaries(t *testing.T) {
	input := append(bytes.Repeat([]byte{'a'}, credentials.MaxAPIKeyBytes), '\r', '\n')
	value, err := readAPIKey(bytes.NewReader(input), &bytes.Buffer{})
	if err != nil || len(value) != credentials.MaxAPIKeyBytes {
		t.Fatalf("max-sized key length=%d error=%v", len(value), err)
	}
	credentials.Clear(value)

	for name, input := range map[string][]byte{
		"empty":     nil,
		"too large": bytes.Repeat([]byte{'a'}, credentials.MaxAPIKeyBytes+1),
		"multiline": []byte("first\nsecond"),
	} {
		t.Run(name, func(t *testing.T) {
			if value, err := readAPIKey(bytes.NewReader(input), &bytes.Buffer{}); err == nil {
				credentials.Clear(value)
				t.Fatal("invalid API key was accepted")
			}
		})
	}
}

func TestReadBoundedTTYLine(t *testing.T) {
	input := append(bytes.Repeat([]byte{'a'}, credentials.MaxAPIKeyBytes+4096), '\r')
	value, err := readBoundedTTYLine(bytes.NewReader(input))
	credentials.Clear(value)
	if err == nil || !strings.Contains(err.Error(), "exceeds 2048 bytes") || len(value) > credentials.MaxAPIKeyBytes || cap(value) > credentials.MaxAPIKeyBytes {
		t.Fatalf("length=%d capacity=%d error=%v", len(value), cap(value), err)
	}
	value, err = readBoundedTTYLine(strings.NewReader("abc\bde\r"))
	defer credentials.Clear(value)
	if err != nil || string(value) != "abde" {
		t.Fatalf("edited value=%q error=%v", value, err)
	}
}

func TestRunRejectsUnknownAuthCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"auth", "nope"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown auth command") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunWritesJSONForFlagErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check", "--output-format", "json", "--unknown"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 1 || result.Errors[0].Kind != "usage" {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRequiresCredentialsBeforeInputDiscovery(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check", "--output-format", "json", "missing.go"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 1 || result.Errors[0].Kind != "config" || result.Errors[0].Path != "" {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunStructuresInputErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check", "--output-format", "json", "missing.go"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 1 || result.Errors[0].Kind != "input" || result.Errors[0].Path != "missing.go" {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunStructuresProviderErrors(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-typesafe-request-id", "request-123")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check", "--no-cache", "--output-format", "json", "sample.go"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 1 || result.Errors[0].Kind != "provider" || result.Errors[0].Path != "sample.go" || result.Errors[0].HTTPStatus != http.StatusUnauthorized || result.Errors[0].RequestID != "request-123" {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunExitCodes(t *testing.T) {
	for name, test := range map[string]struct {
		noul float64
		exit int
	}{
		"pass":         {noul: 0.1, exit: 0},
		"violation":    {noul: 0.9, exit: 1},
		"inconclusive": {noul: 0.5, exit: 2},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			server := noulServer(test.noul)
			defer server.Close()
			t.Setenv("TYPESAFE_API_KEY", "test-key")
			t.Setenv("TYPESAFE_BASE_URL", server.URL)

			var stdout, stderr bytes.Buffer
			exit := run([]string{"check", "--no-cache", "--output-format", "json", "sample.go"}, bytes.NewReader(nil), &stdout, &stderr)
			if exit != test.exit {
				t.Fatalf("exit = %d, want %d", exit, test.exit)
			}
			result := decodeJSONResult(t, stdout.Bytes())
			if len(result.Checks) != cliRuleCount || result.Checks[0].Status != name || result.Checks[1].Status != name {
				t.Fatalf("result = %#v", result)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func noulServer(noul float64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answers := make(map[string]map[string]any, len(request.Questions))
		for code := range request.Questions {
			answers[code] = map[string]any{"type": "noul", "noul": noul}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Model   string                    `json:"model"`
			Answers map[string]map[string]any `json:"answers"`
		}{Model: "jev-1.13.0", Answers: answers})
	}))
}

func decodeJSONResult(t *testing.T, data []byte) jsonResult {
	t.Helper()
	var result jsonResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode JSON %q: %v", data, err)
	}
	return result
}
