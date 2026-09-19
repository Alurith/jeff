package harness

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jeff/internal/rules"
)

func TestMetricsFormulasAndDenominators(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	score := func(value float64) *float64 { return &value }
	observations := []CaseResult{
		metricObservation("clean-pass", "GEN001", LabelClean, "pass", score(0.1), 10),
		metricObservation("clean-fp", "GEN001", LabelClean, "violation", score(0.9), 20),
		metricObservation("violation-tp", "GEN001", LabelViolation, "violation", score(0.9), 30),
		metricObservation("violation-fn", "GEN001", LabelViolation, "pass", score(0.1), 40),
		metricObservation("violation-abstain", "GEN001", LabelViolation, "inconclusive", score(0.5), 50),
		{CaseID: "violation-unavailable", Rule: "GEN001", Label: LabelViolation, TargetStatus: "", Unavailable: true, ProcessValid: false, LatencyMS: 60},
		{CaseID: "ambiguous", Rule: "GEN001", Label: LabelAmbiguous, TargetStatus: "inconclusive", TargetNoul: score(0.5), ProcessValid: true, LatencyMS: 70},
		{CaseID: "not-applicable", Rule: "GEN001", Label: LabelNotApplicable, ProcessValid: true, LatencyMS: 80},
	}
	observations[0].Checks = []ObservedCheck{{Code: "GEN001", Status: "pass", Noul: score(0.1)}, {Code: "GEN002", Status: "violation", Noul: score(0.9)}}
	metrics, err := ComputeMetrics(observations, catalog)
	if err != nil {
		t.Fatal(err)
	}
	global := metrics.Global
	if global.Observations != 6 || global.GoldClean != 2 || global.GoldViolation != 4 || global.TruePositives != 1 || global.FalsePositives != 1 || global.FalseNegatives != 3 || global.Abstain != 1 || global.Unavailable != 1 || global.Scored != 5 {
		t.Fatalf("counts = %#v", global)
	}
	assertFloat(t, global.Precision, 0.5)
	assertFloat(t, global.Recall, 0.25)
	assertFloat(t, global.F05, 5.0/12.0)
	assertFloat(t, global.FalsePositiveRate, 0.5)
	assertFloat(t, global.ScoreCoverage, 5.0/6.0)
	assertFloat(t, global.DecisionCoverage, 4.0/6.0)
	assertFloat(t, global.InconclusiveRate, 1.0/6.0)
	assertFloat(t, global.UnavailableRate, 1.0/6.0)
	assertFloat(t, global.Brier, 1.89/5)
	assertFloat(t, global.Calibration.ECE, 0.42)
	if metrics.Ambiguous != 1 || metrics.NotApplicable != 1 {
		t.Fatalf("non-binary counts = %#v", metrics)
	}
	if len(metrics.CrossRule) != 1 || metrics.CrossRule[0].TargetRule != "GEN001" || metrics.CrossRule[0].ActivatedRule != "GEN002" || metrics.CrossRule[0].Activations != 1 || metrics.CrossRule[0].UnexpectedActivations != 1 || metrics.CrossRule[0].Opportunities != 1 {
		t.Fatalf("cross-rule = %#v", metrics.CrossRule)
	}
	if metrics.Latency.Count != len(observations) {
		t.Fatalf("latency count = %d", metrics.Latency.Count)
	}

	empty, err := ComputeMetrics(nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Global.Precision != nil || empty.Global.Brier != nil || empty.Global.Calibration.ECE != nil {
		t.Fatalf("empty denominators = %#v", empty.Global)
	}
	if len(empty.Global.Calibration.Bins) != 10 {
		t.Fatalf("empty calibration bins = %d", len(empty.Global.Calibration.Bins))
	}
}

func TestMetricsAllowlistedActivationRemainsVisible(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	score := 0.1
	metrics, err := ComputeMetrics([]CaseResult{{CaseID: "allowlisted", Rule: "GEN001", Label: LabelClean, TargetStatus: "pass", TargetNoul: &score, ProcessValid: true, Checks: []ObservedCheck{{Code: "GEN001", Status: "pass"}, {Code: "GEN002", Status: "violation"}}, AllowedActivations: []string{"GEN002"}, Attempts: 1}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.CrossRule) != 1 || metrics.CrossRule[0].Activations != 1 || metrics.CrossRule[0].AllowlistedActivations != 1 || metrics.CrossRule[0].UnexpectedActivations != 0 {
		t.Fatalf("allowlisted activation = %#v", metrics.CrossRule)
	}
}

func TestMetricsPairsAndPercentiles(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	score := func(value float64) *float64 { return &value }
	observations := []CaseResult{
		pairObservation("pair-one-clean-1", "pair-one", 1, LabelClean, "pass", score(0.1)),
		pairObservation("pair-one-violation-1", "pair-one", 1, LabelViolation, "violation", score(0.9)),
		pairObservation("pair-one-clean-2", "pair-one", 2, LabelClean, "inconclusive", score(0.5)),
		{CaseID: "pair-one-violation-2", Rule: "GEN001", Label: LabelViolation, Pair: "pair-one", Repetition: 2, Unavailable: true, ProcessValid: false},
		pairObservation("pair-two-clean-1", "pair-two", 1, LabelClean, "pass", score(0.1)),
		pairObservation("pair-two-violation-1", "pair-two", 1, LabelViolation, "pass", score(0.1)),
	}
	for index := range observations {
		observations[index].LatencyMS = int64((index + 1) * 10)
		observations[index].ProviderLatencyMS = int64((index + 1) * 5)
	}
	metrics, err := ComputeMetrics(observations, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Pairs.Expected != 3 || metrics.Pairs.Successful != 1 || metrics.Pairs.Covered != 2 || metrics.Pairs.Ordered != 1 {
		t.Fatalf("pairs = %#v", metrics.Pairs)
	}
	assertFloat(t, metrics.Pairs.FlipRate, 1.0/3.0)
	assertFloat(t, metrics.Pairs.PairCoverage, 2.0/3.0)
	assertFloat(t, metrics.Pairs.OrderRate, 0.5)
	assertFloat(t, metrics.Pairs.MeanDelta, 0.4)
	assertFloat(t, metrics.Latency.P50MS, 30)
	assertFloat(t, metrics.Latency.P95MS, 60)
	assertFloat(t, metrics.Latency.P99MS, 60)
	assertFloat(t, metrics.Latency.ProviderP50MS, 15)
}

func TestReportWritesSummaryWithoutRawSource(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	score := 0.1
	observations := []CaseResult{{CaseID: "case", Rule: "GEN001", Label: LabelClean, TargetStatus: "pass", TargetNoul: &score, ProcessValid: true, Filename: "sample.go", SourceSHA256: "hash", LatencyMS: 1}}
	metrics, err := ComputeMetrics(observations, catalog)
	if err != nil {
		t.Fatal(err)
	}
	result := RunResults{Metadata: RunMetadata{DatasetName: "dataset", DatasetVersion: "1.0.0", Split: "dev", Provider: "synthetic", Model: "jev-1.13.0", Profile: "smoke", Repetitions: 1}, Cases: observations, Metrics: &metrics}
	output := t.TempDir()
	if err := writeSummary(output, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"Global metrics", "precision", "score coverage", "dataset"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("summary missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "package sample") || strings.Contains(text, "Authorization") {
		t.Fatal("summary contains raw data")
	}
}

func metricObservation(id, rule string, label Label, status string, score *float64, latency int64) CaseResult {
	return CaseResult{CaseID: id, Rule: rule, Label: label, TargetStatus: status, TargetNoul: score, ProcessValid: true, LatencyMS: latency}
}

func pairObservation(id, pair string, repetition int, label Label, status string, score *float64) CaseResult {
	return CaseResult{CaseID: id, Rule: "GEN001", Label: label, Pair: pair, Repetition: repetition, TargetStatus: status, TargetNoul: score, ProcessValid: true}
}

func assertFloat(t *testing.T, actual *float64, expected float64) {
	t.Helper()
	if actual == nil || math.Abs(*actual-expected) > 1e-9 {
		t.Fatalf("got %v, want %v", actual, expected)
	}
}
