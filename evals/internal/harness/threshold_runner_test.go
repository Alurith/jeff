package harness

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRunnerSeparatesJeffAndEvalThresholdStatuses(t *testing.T) {
	root := repoRoot(t)
	binary := buildJeff(t, root)
	config, err := LoadConfig(filepath.Join(root, "evals", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	passBelow, failAtOrAbove := 0.01, 0.95
	config.Thresholds.Overrides = map[string]Threshold{"GEN001": {PassBelow: &passBelow, FailAtOrAbove: &failAtOrAbove}}
	source := "package threshold\n"
	manifest := writeManifest(t, t.TempDir(), "cases.yaml", "dev", false, []Case{{ID: "threshold-clean", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Tags: []string{"smoke"}, Source: Source{Filename: "threshold.go", Inline: &source}}})
	dataset, err := LoadDataset(manifest)
	if err != nil {
		t.Fatal(err)
	}
	results, err := Run(context.Background(), RunOptions{Dataset: dataset, Config: config, JeffBinary: binary, RepoRoot: root, Profile: "smoke", Provider: "synthetic", OutputDir: filepath.Join(t.TempDir(), "out")})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Cases) != 1 {
		t.Fatalf("cases = %d", len(results.Cases))
	}
	item := results.Cases[0]
	if item.JeffStatus != "pass" || item.EvalStatus != "inconclusive" {
		t.Fatalf("threshold statuses = %#v", item)
	}
	if results.ExitCode() != 1 || len(results.GateFailures) == 0 {
		t.Fatalf("exit code=%d gate failures=%#v", results.ExitCode(), results.GateFailures)
	}
}
