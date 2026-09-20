package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"jeff/internal/rules"
)

func TestRunnerSyntheticEndToEnd(t *testing.T) {
	root := repoRoot(t)
	binary := buildJeff(t, root)
	config, err := LoadConfig(filepath.Join(root, "evals", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	clean := "package clean\n"
	violation := "package violation\nfunc changed() {}\n"
	ambiguous := "package ambiguous\nvar value = 1\n"
	notApplicable := "# documentation\n"
	manifest := writeManifest(t, t.TempDir(), "cases.yaml", "dev", false, []Case{
		{ID: "clean-case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Tags: []string{"smoke"}, Source: Source{Filename: "clean.go", Inline: &clean}},
		{ID: "violation-case", Rule: "GEN003", Language: "go", Label: LabelViolation, Difficulty: DifficultyEasy, Tags: []string{"smoke"}, Source: Source{Filename: "violation.go", Inline: &violation}},
		{ID: "ambiguous-case", Rule: "GEN002", Language: "go", Label: LabelAmbiguous, Difficulty: DifficultyMedium, Rationale: "score is intentionally inconclusive", Tags: []string{"smoke"}, Source: Source{Filename: "ambiguous.go", Inline: &ambiguous}},
		{ID: "not-applicable-case", Rule: "GEN002", Language: "markdown", Label: LabelNotApplicable, Difficulty: DifficultyEasy, Rationale: "GEN002 excludes markdown files", Tags: []string{"smoke"}, Source: Source{Filename: "notes.md", Inline: &notApplicable}},
	})
	dataset, err := LoadDataset(manifest)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "out")
	results, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: binary, RepoRoot: root, Profile: "smoke", Provider: "synthetic", OutputDir: output})
	if err != nil {
		t.Fatal(err)
	}
	if got := results.ExitCode(); got != 0 {
		t.Fatalf("runner exit = %d, results = %#v", got, results)
	}
	if len(results.Cases) != 4 {
		t.Fatalf("got %d case results", len(results.Cases))
	}
	byID := make(map[string]CaseResult, len(results.Cases))
	for _, item := range results.Cases {
		byID[item.CaseID] = item
		if !item.ProcessValid || item.Unavailable {
			t.Fatalf("invalid observation for %s: %#v", item.CaseID, item)
		}
	}
	if byID["clean-case"].TargetStatus != "pass" || byID["clean-case"].JeffExitCode != 0 || byID["clean-case"].Attempts != 1 {
		t.Fatalf("clean result = %#v", byID["clean-case"])
	}
	if byID["violation-case"].TargetStatus != "violation" || byID["violation-case"].JeffExitCode != 1 {
		t.Fatalf("violation result = %#v", byID["violation-case"])
	}
	if byID["ambiguous-case"].TargetStatus != "inconclusive" || byID["ambiguous-case"].JeffExitCode != 2 {
		t.Fatalf("ambiguous result = %#v", byID["ambiguous-case"])
	}
	if byID["not-applicable-case"].TargetStatus != "" || byID["not-applicable-case"].Attempts != 0 {
		t.Fatalf("not-applicable result = %#v", byID["not-applicable-case"])
	}
	data, err := os.ReadFile(filepath.Join(output, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if strings.Contains(contents, "package clean") || strings.Contains(contents, syntheticAPIKey) {
		t.Fatal("results contain source or synthetic credential")
	}
	var persisted RunResults
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata.BinarySHA256 == "" || persisted.Metadata.CatalogHash == "" || persisted.Metadata.SelectionHash == "" {
		t.Fatalf("missing provenance: %#v", persisted.Metadata)
	}
}

func TestSyntheticProviderRejectsWrongPayload(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	source := "package sample\n"
	manifest := writeManifest(t, t.TempDir(), "cases.yaml", "dev", false, []Case{{ID: "case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Source: Source{Filename: "sample.go", Inline: &source}}})
	dataset, err := LoadDataset(manifest)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewSyntheticProvider(catalog, catalog.Model, dataset)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	request, err := http.NewRequest(http.MethodPost, provider.URL()+"/v1/systemone", strings.NewReader(`{"state":"wrong","model":"jev-1.13.0","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+syntheticAPIKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, body = %s", response.StatusCode, body)
	}
}

func TestRunnerRejectsEmptySelectionAndNonEmptyOutput(t *testing.T) {
	root := repoRoot(t)
	config, err := LoadConfig(filepath.Join(root, "evals", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	source := "package sample\n"
	manifest := writeManifest(t, t.TempDir(), "cases.yaml", "dev", false, []Case{{ID: "case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Source: Source{Filename: "sample.go", Inline: &source}}})
	dataset, err := LoadDataset(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: filepath.Join(root, "missing-jeff"), Profile: "smoke", Provider: "synthetic", OutputDir: filepath.Join(t.TempDir(), "out")}); err == nil || !strings.Contains(err.Error(), "selected no cases") {
		t.Fatalf("empty selection error = %v", err)
	}
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "old.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: filepath.Join(root, "missing-jeff"), Profile: "all", Provider: "synthetic", OutputDir: output}); err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("non-empty output error = %v", err)
	}
}

func buildJeff(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "jeff")
	command := exec.Command("go", "build", "-o", binary, "./cmd/jeff")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Jeff: %v\n%s", err, output)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}
