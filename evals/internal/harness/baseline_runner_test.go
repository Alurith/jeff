package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunnerBaselineComparison(t *testing.T) {
	root := repoRoot(t)
	binary := buildJeff(t, root)
	dataset, err := LoadDataset(filepath.Join(root, "evals", "datasets", "dev", "cases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(filepath.Join(root, "evals", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: binary, RepoRoot: root, Profile: "smoke", Provider: "synthetic", OutputDir: filepath.Join(t.TempDir(), "first")})
	if err != nil {
		t.Fatal(err)
	}
	baseline := cloneRunResults(t, first)
	baseline.Metadata.WorktreeDirty = false
	baselinePath := filepath.Join(t.TempDir(), "synthetic-baseline.json")
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baselinePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	approved, err := LoadBaseline(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: binary, RepoRoot: root, Profile: "smoke", Provider: "synthetic", OutputDir: filepath.Join(t.TempDir(), "second")})
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachBaseline(&candidate, approved, config.Gates); err != nil {
		t.Fatal(err)
	}
	if candidate.Baseline == nil || !candidate.Baseline.Passed {
		t.Fatalf("baseline comparison = %#v", candidate.Baseline)
	}
}
