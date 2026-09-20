package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jeff/internal/rules"
)

func TestBaselineCompatibilityAndRegression(t *testing.T) {
	baseline := baselineFixture(t)
	candidate := cloneRunResults(t, baseline)
	gates := baselineGates()
	comparison, err := CompareBaseline(baseline, candidate, gates)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Passed || len(comparison.Regressions) != 0 {
		t.Fatalf("comparison = %#v", comparison)
	}

	candidate = cloneRunResults(t, baseline)
	candidate.Cases[0].EvalStatus = "violation"
	candidate.Cases[0].TargetNoul = floatPtr(0.9)
	candidate.Metrics.Global.FalsePositives = 1
	candidate.Metrics.Global.Precision = floatPtr(0.5)
	candidate.Metrics.PerRule["GEN001"] = RuleMetrics{Binary: candidate.Metrics.Global}
	comparison, err = CompareBaseline(baseline, candidate, gates)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Passed || !hasRegression(comparison.Regressions, "new_false_positive") {
		t.Fatalf("expected false-positive regression: %#v", comparison)
	}

	candidate = cloneRunResults(t, baseline)
	candidate.Metadata.ConfigHash = "different"
	var mismatch *BaselineMismatch
	if _, err := CompareBaseline(baseline, candidate, gates); !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want BaselineMismatch", err)
	}
}

func TestBaselineRejectsDirtyAndWritesRoundTrip(t *testing.T) {
	baseline := baselineFixture(t)
	path := filepath.Join(t.TempDir(), "baseline.json")
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Metadata.DatasetHash != baseline.Metadata.DatasetHash {
		t.Fatalf("loaded baseline = %#v", loaded.Metadata)
	}
	baseline.Metadata.WorktreeDirty = true
	dirty, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, dirty, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "dirty worktree") {
		t.Fatalf("dirty baseline error = %v", err)
	}
	baseline.Metadata.WorktreeDirty = false
	baseline.SchemaVersion = ResultsSchemaVersion - 1
	oldSchema, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, oldSchema, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "unsupported result schema") {
		t.Fatalf("old schema error = %v", err)
	}
}

func TestBaselineNullRequiredMetricFailsClosed(t *testing.T) {
	baseline := baselineFixture(t)
	candidate := cloneRunResults(t, baseline)
	candidate.Metrics.Global.ScoreCoverage = nil
	candidate.Metrics.PerRule["GEN001"] = RuleMetrics{Binary: candidate.Metrics.Global}
	if _, err := CompareBaseline(baseline, candidate, baselineGates()); err == nil || !strings.Contains(err.Error(), "score_coverage") {
		t.Fatalf("expected invalid null metric error, got %v", err)
	}
}

func baselineFixture(t *testing.T) RunResults {
	t.Helper()
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	precision, recall, f05, coverage, decision, brier := 1.0, 1.0, 1.0, 1.0, 1.0, 0.01
	global := BinaryMetrics{Observations: 2, GoldClean: 1, GoldViolation: 1, TruePositives: 1, Scored: 2, Conclusive: 2, Precision: &precision, Recall: &recall, F05: &f05, ScoreCoverage: &coverage, DecisionCoverage: &decision, InconclusiveRate: floatPtr(0), UnavailableRate: floatPtr(0), Brier: &brier}
	pairs := PairMetrics{Expected: 1, Successful: 1, Covered: 1, Ordered: 1, FlipRate: floatPtr(1), PairCoverage: floatPtr(1), OrderRate: floatPtr(1), MeanDelta: floatPtr(0.8)}
	perRule := make(map[string]RuleMetrics, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		perRule[rule.Code] = RuleMetrics{}
	}
	perRule["GEN001"] = RuleMetrics{Binary: global}
	return RunResults{
		SchemaVersion:  ResultsSchemaVersion,
		MetricsVersion: 1,
		Metadata:       RunMetadata{DatasetHash: "dataset", DatasetName: "test", DatasetVersion: "1.0.0", Split: "test", SelectionHash: "selection", Profile: "smoke", Provider: "synthetic", Model: "jev-1.13.0", Repetitions: 1, BinarySHA256: "binary", CatalogHash: "catalog", ConfigHash: "config", EffectiveThresholdHash: "thresholds", WorktreeDirty: false},
		Cases: []CaseResult{
			{CaseID: "clean", Rule: "GEN001", Label: LabelClean, Pair: "pair", Repetition: 1, EvalStatus: "pass", TargetNoul: floatPtr(0.1), ProcessValid: true},
			{CaseID: "violation", Rule: "GEN001", Label: LabelViolation, Pair: "pair", Repetition: 1, EvalStatus: "violation", TargetNoul: floatPtr(0.9), ProcessValid: true},
		},
		Metrics: &Metrics{Observations: 2, Global: global, PerRule: perRule, Pairs: pairs},
	}
}

func baselineGates() Gates {
	return Gates{MinCases: 1, MinScoreCoverage: 1, MaxUnavailableRate: 0, MaxInconclusiveRate: 0, MinRecall: 0.8, MinFlipRate: 0.8, MaxBrier: floatPtr(0.15), MaxNewFalsePositives: 0, MaxNewMissedViolations: 0, MaxNewActivations: 0, MaxScoreCoverageDrop: 0, MaxFlipRateDrop: 0, MaxInconclusiveRateIncrease: 0, MaxUnavailableRateIncrease: 0, MaxBrierIncrease: 0.02}
}

func cloneRunResults(t *testing.T, result RunResults) RunResults {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var clone RunResults
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func hasRegression(regressions []Regression, kind string) bool {
	for _, regression := range regressions {
		if regression.Kind == kind {
			return true
		}
	}
	return false
}

func floatPtr(value float64) *float64 { return &value }
