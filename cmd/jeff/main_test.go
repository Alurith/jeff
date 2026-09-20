package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"

	"jeff/internal/credentials"
	"jeff/internal/selfupdate"
)

const cliRuleCount = 4

func TestRunPrintsVersionWithoutInitializingCLI(t *testing.T) {
	previous := version
	version = "v1.2.3"
	t.Cleanup(func() { version = previous })

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"--version"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.String() != "v1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunUpdatesReleasedBinary(t *testing.T) {
	previousVersion, previousUpdate := version, update
	version = "v1.0.0"
	update = func(_ context.Context, current string) (selfupdate.Result, error) {
		if current != "v1.0.0" {
			t.Fatalf("current version = %q", current)
		}
		return selfupdate.Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", Updated: true}, nil
	}
	t.Cleanup(func() {
		version = previousVersion
		update = previousUpdate
	})

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"update"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if stdout.String() != "Updated jeff from 1.0.0 to 1.1.0.\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunReportsUpdateFailure(t *testing.T) {
	previousUpdate := update
	update = func(context.Context, string) (selfupdate.Result, error) {
		return selfupdate.Result{}, selfupdate.ErrDevelopmentBuild
	}
	t.Cleanup(func() { update = previousUpdate })

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"update"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "self-update is unavailable") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type jsonResult struct {
	Checks []struct {
		Path   string `json:"path"`
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

func TestRunWritesJSONForUsageErrors(t *testing.T) {
	for name, test := range map[string]struct {
		args []string
		want string
	}{
		"flag": {
			[]string{"check", "--output-format", "json", "--unknown"},
			`{"schema_version":1,"checks":[],"errors":[{"kind":"usage","message":"flag provided but not defined: -unknown"}],"warnings":[]}` + "\n",
		},
		"command": {
			[]string{"unknown", "--output-format", "json"},
			`{"schema_version":1,"checks":[],"errors":[{"kind":"usage","message":"unknown command \"unknown\""}],"warnings":[]}` + "\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if exit := run(test.args, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
				t.Fatalf("exit = %d, want 2", exit)
			}
			if stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
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

func TestRunUsesProjectConfigForSourcesOutputAndCache(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"src/sample.go", "docs/readme.go"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "jeff.toml"), []byte("src = [\"src\"]\noutput-format = \"json\"\ncache-dir = \"custom-cache\"\njev-version = \"jev-2.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := noulServer(0.01)
	defer server.Close()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", server.URL)
	t.Setenv("JEFF_CACHE_DIR", "")

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 0 || len(result.Checks) != cliRuleCount || result.Checks[0].Path != "src/sample.go" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "custom-cache", "v1")); err != nil {
		t.Fatalf("cache directory was not configured: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := run([]string{"check", "docs/readme.go", "--no-cache", "--output-format", "json"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("explicit path exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	explicit := decodeJSONResult(t, stdout.Bytes())
	if len(explicit.Checks) != cliRuleCount || explicit.Checks[0].Path != "docs/readme.go" || stderr.Len() != 0 {
		t.Fatalf("explicit result=%#v stderr=%q", explicit, stderr.String())
	}
}

func TestPartitionCheckArgs(t *testing.T) {
	flagArgs, paths, format, formatSet := partitionCheckArgs([]string{"src/main.go", "--config", "jeff.toml", "--output-format=json", "--no-cache", "--", "-literal.go"})
	if strings.Join(flagArgs, " ") != "--config jeff.toml --output-format=json --no-cache" || strings.Join(paths, " ") != "src/main.go -literal.go" || format != "json" || !formatSet {
		t.Fatalf("flags=%#v paths=%#v format=%q set=%v", flagArgs, paths, format, formatSet)
	}
}

func TestRunUsesJSONForConfigErrors(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "jeff.toml"), []byte("output-format = \"json\"\nunknown = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check"}, bytes.NewReader(nil), &stdout, &stderr); exit != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 1 || result.Errors[0].Kind != "config" || stderr.Len() != 0 {
		t.Fatalf("result=%#v stderr=%q", result, stderr.String())
	}
}

func TestRunAcceptsFlagsAfterPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jeff.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	server := noulServer(0.01)
	defer server.Close()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check", "sample.go", "--config", "jeff.toml", "--output-format", "json", "--no-cache"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Checks) != cliRuleCount || stderr.Len() != 0 {
		t.Fatalf("result=%#v stderr=%q", result, stderr.String())
	}
}

func TestRunUsesConfiguredExternalRulesAndIncludes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{
		"src/main.go",
		"lib/helper.go",
		"docs/readme.go",
		"generated/generated.go",
		"tools/tool.go",
		"src/.git/config.go",
		"src/.jeff-cache/v1/entry.go",
		"src/node_modules/package/index.js",
		"src/vendor/dependency.go",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "team.yml"), []byte(`family: TEAM
rules:
  - code: TEAM001
    name: custom-check
    message: Custom rule
    scope: file
    files:
      include:
        - "**/*.go"
      allow-non-source: true
    question:
      type: noul
      instructions: The file satisfies the custom condition.
    decision:
      pass_below: 0.20
      fail_at_or_above: 0.80
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jeff.toml"), []byte(`rule-files = ["rules/team.yml"]
src = ["src", "lib"]
exclude = ["docs", "generated/**"]
include = ["tools/tool.go"]
cache-dir = "toml-cache"
output-format = "json"
jev-version = "jev-2.0.0"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "jev-2.0.0" || len(request.Questions) != cliRuleCount+1 {
			t.Errorf("request model=%q questions=%d", request.Model, len(request.Questions))
		}
		if _, ok := request.Questions["TEAM001"]; !ok {
			t.Error("TEAM001 was not requested")
		}
		requests.Add(1)
		answers := make(map[string]map[string]any, len(request.Questions))
		for code := range request.Questions {
			answers[code] = map[string]any{"type": "noul", "noul": 0.01}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Model   string                    `json:"model"`
			Answers map[string]map[string]any `json:"answers"`
		}{Model: request.Model, Answers: answers})
	}))
	defer server.Close()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", server.URL)
	t.Setenv("JEFF_CACHE_DIR", "  env-cache  ")

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"check"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	result := decodeJSONResult(t, stdout.Bytes())
	if len(result.Errors) != 0 || len(result.Checks) != 3*(cliRuleCount+1) {
		t.Fatalf("result=%#v", result)
	}
	paths := map[string]bool{}
	for _, check := range result.Checks {
		paths[check.Path] = true
	}
	for _, path := range []string{"src/main.go", "lib/helper.go", "tools/tool.go"} {
		if !paths[path] {
			t.Fatalf("missing checked path %q: %#v", path, paths)
		}
	}
	for _, path := range []string{"docs/readme.go", "generated/generated.go", "src/.git/config.go", "src/.jeff-cache/v1/entry.go", "src/node_modules/package/index.js", "src/vendor/dependency.go"} {
		if paths[path] {
			t.Fatalf("excluded path was checked: %q", path)
		}
	}
	if requests.Load() != 1 || stderr.Len() != 0 {
		t.Fatalf("requests=%d stderr=%q", requests.Load(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "env-cache", "v1")); err != nil {
		t.Fatalf("environment cache directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "toml-cache")); !os.IsNotExist(err) {
		t.Fatalf("TOML cache directory was used: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if exit := run([]string{"check", "--output-format", "text", "--no-cache"}, bytes.NewReader(nil), &stdout, &stderr); exit != 0 || !strings.Contains(stdout.String(), "0 violation(s)") {
		t.Fatalf("CLI output override exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunExitCodes(t *testing.T) {
	for name, test := range map[string]struct {
		noul float64
		exit int
	}{
		"pass":         {noul: 0.01, exit: 0},
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
			if len(result.Checks) != cliRuleCount {
				t.Fatalf("result = %#v", result)
			}
			if name != "inconclusive" && (result.Checks[0].Status != name || result.Checks[1].Status != name) {
				t.Fatalf("result = %#v", result)
			}
			if name == "inconclusive" {
				found := false
				for _, check := range result.Checks {
					if check.Status == name {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("result = %#v", result)
				}
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
			Model     string                     `json:"model"`
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
		}{Model: request.Model, Answers: answers})
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
