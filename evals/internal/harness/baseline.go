package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type BaselineComparison struct {
	Passed      bool         `json:"passed"`
	Regressions []Regression `json:"regressions"`
}

type Regression struct {
	Scope     string   `json:"scope"`
	Rule      string   `json:"rule,omitempty"`
	CaseID    string   `json:"case_id,omitempty"`
	Pair      string   `json:"pair,omitempty"`
	Kind      string   `json:"kind"`
	Baseline  *float64 `json:"baseline,omitempty"`
	Candidate *float64 `json:"candidate,omitempty"`
	Limit     *float64 `json:"limit,omitempty"`
}

type BaselineMismatch struct {
	Field string
	Want  string
	Got   string
}

type MetricInvalid struct {
	Scope string
	Field string
}

func (e *MetricInvalid) Error() string {
	return fmt.Sprintf("invalid comparison metric: %s.%s is unavailable", e.Scope, e.Field)
}

func (e *BaselineMismatch) Error() string {
	return fmt.Sprintf("baseline incompatible: %s differs (baseline %q, candidate %q)", e.Field, e.Want, e.Got)
}

func LoadBaseline(filename string) (RunResults, error) {
	path, err := cleanRegularPath(filename)
	if err != nil {
		return RunResults{}, fmt.Errorf("baseline: %w", err)
	}
	data, err := readBoundedFile(path, 16<<20)
	if err != nil {
		return RunResults{}, fmt.Errorf("baseline %s: %w", filename, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result RunResults
	if err := decoder.Decode(&result); err != nil {
		return RunResults{}, fmt.Errorf("baseline %s: decode JSON: %w", filename, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return RunResults{}, fmt.Errorf("baseline %s: multiple JSON documents", filename)
		}
		return RunResults{}, fmt.Errorf("baseline %s: trailing JSON: %w", filename, err)
	}
	if err := validateBaseline(result); err != nil {
		return RunResults{}, fmt.Errorf("baseline %s: %w", filename, err)
	}
	return result, nil
}

func validateBaseline(result RunResults) error {
	if result.SchemaVersion != ResultsSchemaVersion {
		return fmt.Errorf("unsupported result schema %d", result.SchemaVersion)
	}
	if result.MetricsVersion != 1 {
		return fmt.Errorf("unsupported metrics version %d", result.MetricsVersion)
	}
	if result.Metrics == nil {
		return fmt.Errorf("metrics are required")
	}
	if result.Metadata.DatasetHash == "" || result.Metadata.SelectionHash == "" || result.Metadata.CatalogHash == "" || result.Metadata.ConfigHash == "" || result.Metadata.EffectiveThresholdHash == "" || result.Metadata.Model == "" || result.Metadata.Provider == "" || result.Metadata.Split == "" || result.Metadata.Repetitions < 1 {
		return fmt.Errorf("required metadata is missing")
	}
	if result.Metadata.WorktreeDirty {
		return fmt.Errorf("dirty worktree cannot be an approved baseline")
	}
	if len(result.GateFailures) > 0 || (result.Baseline != nil && !result.Baseline.Passed) {
		return fmt.Errorf("baseline contains failed gates")
	}
	seen := make(map[string]struct{}, len(result.Cases))
	for _, item := range result.Cases {
		key := item.CaseID + "\x00" + fmt.Sprint(item.Repetition)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate case observation %s", item.CaseID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func AttachBaseline(candidate *RunResults, baseline RunResults, gates Gates) error {
	comparison, err := CompareBaseline(baseline, *candidate, gates)
	if err != nil {
		return err
	}
	candidate.Baseline = &comparison
	return nil
}

func CompareBaseline(baseline, candidate RunResults, gates Gates) (BaselineComparison, error) {
	if err := compatibleBaseline(baseline, candidate); err != nil {
		return BaselineComparison{}, err
	}
	if err := validateComparisonMetrics("baseline", baseline.Metrics, gates, nil); err != nil {
		return BaselineComparison{}, err
	}
	if err := validateComparisonMetrics("candidate", candidate.Metrics, gates, baseline.Metrics); err != nil {
		return BaselineComparison{}, err
	}
	comparison := BaselineComparison{Regressions: []Regression{}}
	baselineCases := caseIndex(baseline.Cases)
	candidateCases := caseIndex(candidate.Cases)
	for key, before := range baselineCases {
		after, ok := candidateCases[key]
		if !ok {
			return BaselineComparison{}, fmt.Errorf("baseline incompatible: missing candidate observation %s", key)
		}
		compareCaseTransitions(&comparison.Regressions, before, after)
	}
	if len(baselineCases) != len(baseline.Cases) || len(candidateCases) != len(candidate.Cases) {
		return BaselineComparison{}, fmt.Errorf("baseline incompatible: duplicate case observation")
	}
	for key := range candidateCases {
		if _, ok := baselineCases[key]; !ok {
			return BaselineComparison{}, fmt.Errorf("baseline incompatible: unexpected candidate observation %s", key)
		}
	}
	if candidate.Metrics.Global.Observations < gates.MinCases {
		comparison.Regressions = append(comparison.Regressions, countRegression("global", "", "too_few_observations", baseline.Metrics.Global.Observations, candidate.Metrics.Global.Observations, gates.MinCases))
	}
	compareBinaryGates(&comparison.Regressions, "global", "", baseline.Metrics.Global, candidate.Metrics.Global, gates)
	comparePairGates(&comparison.Regressions, baseline.Metrics.Pairs, candidate.Metrics.Pairs, gates)
	compareRuleGates(&comparison.Regressions, baseline.Metrics.PerRule, candidate.Metrics.PerRule, gates)
	compareCrossRules(&comparison.Regressions, baseline.Metrics.CrossRule, candidate.Metrics.CrossRule, gates)
	comparePerformanceGates(&comparison.Regressions, *baseline.Metrics, *candidate.Metrics, gates)
	comparison.Passed = len(comparison.Regressions) == 0
	return comparison, nil
}

func EvaluateGates(metrics Metrics, gates Gates) ([]Regression, error) {
	if err := validateComparisonMetrics("candidate", &metrics, gates, nil); err != nil {
		return nil, err
	}
	regressions := []Regression{}
	if metrics.Global.Observations < gates.MinCases {
		regressions = append(regressions, countRegression("global", "", "too_few_observations", metrics.Global.Observations, metrics.Global.Observations, gates.MinCases))
	}
	compareBinaryGates(&regressions, "global", "", metrics.Global, metrics.Global, gates)
	comparePairGates(&regressions, metrics.Pairs, metrics.Pairs, gates)
	compareRuleGates(&regressions, metrics.PerRule, metrics.PerRule, gates)
	compareCrossRules(&regressions, nil, metrics.CrossRule, gates)
	return regressions, nil
}

func comparePerformanceGates(regressions *[]Regression, before, after Metrics, gates Gates) {
	if gates.MaxP95LatencyIncreaseMS != nil && before.Latency.P95MS != nil && after.Latency.P95MS != nil && increase(before.Latency.P95MS, after.Latency.P95MS) > *gates.MaxP95LatencyIncreaseMS {
		*regressions = append(*regressions, deltaRegression("global", "", "p95_latency_increase", before.Latency.P95MS, after.Latency.P95MS, *gates.MaxP95LatencyIncreaseMS))
	}
	if gates.MaxCostIncreaseUSD != nil && before.Cost.CostUSD != nil && after.Cost.CostUSD != nil && increase(before.Cost.CostUSD, after.Cost.CostUSD) > *gates.MaxCostIncreaseUSD {
		*regressions = append(*regressions, deltaRegression("global", "", "cost_increase", before.Cost.CostUSD, after.Cost.CostUSD, *gates.MaxCostIncreaseUSD))
	}
}

func compatibleBaseline(baseline, candidate RunResults) error {
	if err := validateBaseline(baseline); err != nil {
		return err
	}
	if candidate.SchemaVersion != baseline.SchemaVersion {
		return &BaselineMismatch{Field: "schema_version", Want: fmt.Sprint(baseline.SchemaVersion), Got: fmt.Sprint(candidate.SchemaVersion)}
	}
	if candidate.MetricsVersion != baseline.MetricsVersion {
		return &BaselineMismatch{Field: "metrics_version", Want: fmt.Sprint(baseline.MetricsVersion), Got: fmt.Sprint(candidate.MetricsVersion)}
	}
	fields := []struct {
		name string
		want string
		got  string
	}{
		{"dataset_hash", baseline.Metadata.DatasetHash, candidate.Metadata.DatasetHash},
		{"dataset_name", baseline.Metadata.DatasetName, candidate.Metadata.DatasetName},
		{"dataset_version", baseline.Metadata.DatasetVersion, candidate.Metadata.DatasetVersion},
		{"split", baseline.Metadata.Split, candidate.Metadata.Split},
		{"selection_hash", baseline.Metadata.SelectionHash, candidate.Metadata.SelectionHash},
		{"profile", baseline.Metadata.Profile, candidate.Metadata.Profile},
		{"provider", baseline.Metadata.Provider, candidate.Metadata.Provider},
		{"model", baseline.Metadata.Model, candidate.Metadata.Model},
		{"repetitions", fmt.Sprint(baseline.Metadata.Repetitions), fmt.Sprint(candidate.Metadata.Repetitions)},
		{"catalog_hash", baseline.Metadata.CatalogHash, candidate.Metadata.CatalogHash},
		{"config_hash", baseline.Metadata.ConfigHash, candidate.Metadata.ConfigHash},
		{"effective_threshold_hash", baseline.Metadata.EffectiveThresholdHash, candidate.Metadata.EffectiveThresholdHash},
	}
	for _, field := range fields {
		if field.want != field.got {
			return &BaselineMismatch{Field: field.name, Want: field.want, Got: field.got}
		}
	}
	return nil
}

func caseIndex(cases []CaseResult) map[string]CaseResult {
	result := make(map[string]CaseResult, len(cases))
	for _, item := range cases {
		result[item.CaseID+"\x00"+fmt.Sprint(item.Repetition)] = item
	}
	return result
}

func compareCaseTransitions(regressions *[]Regression, before, after CaseResult) {
	if before.Label == LabelClean && after.Label == LabelClean && evaluatedStatus(before) != "violation" && evaluatedStatus(after) == "violation" {
		*regressions = append(*regressions, Regression{Scope: "case", Rule: after.Rule, CaseID: after.CaseID, Kind: "new_false_positive"})
	}
	if before.Label == LabelViolation && after.Label == LabelViolation && evaluatedStatus(before) == "violation" && evaluatedStatus(after) != "violation" {
		*regressions = append(*regressions, Regression{Scope: "case", Rule: after.Rule, CaseID: after.CaseID, Kind: "missed_violation"})
	}
	if !before.Unavailable && after.Unavailable {
		*regressions = append(*regressions, Regression{Scope: "case", Rule: after.Rule, CaseID: after.CaseID, Kind: "new_unavailable"})
	}
	if evaluatedStatus(before) != "inconclusive" && evaluatedStatus(after) == "inconclusive" {
		*regressions = append(*regressions, Regression{Scope: "case", Rule: after.Rule, CaseID: after.CaseID, Kind: "new_inconclusive"})
	}
}

func validateComparisonMetrics(scope string, metrics *Metrics, gates Gates, expected *Metrics) error {
	if metrics == nil {
		return fmt.Errorf("%s metrics are required", scope)
	}
	if metrics.Global.Observations == 0 {
		return &MetricInvalid{Scope: scope, Field: "global.observations"}
	}
	if err := validateBinaryMetrics(scope+".global", metrics.Global, gates); err != nil {
		return err
	}
	if expected != nil {
		for code, before := range expected.PerRule {
			after, ok := metrics.PerRule[code]
			if !ok {
				return fmt.Errorf("baseline incompatible: missing candidate rule metrics %s", code)
			}
			if before.Binary.Observations > 0 && after.Binary.Observations == 0 {
				return &MetricInvalid{Scope: scope + ".rule." + code, Field: "binary.observations"}
			}
		}
	}
	for code, rule := range metrics.PerRule {
		if rule.Binary.Observations == 0 {
			continue
		}
		if err := validateBinaryMetrics(scope+".rule."+code, rule.Binary, gates); err != nil {
			return err
		}
	}
	if metrics.Pairs.Expected > 0 {
		if metrics.Pairs.FlipRate == nil {
			return &MetricInvalid{Scope: scope + ".pairs", Field: "flip_rate"}
		}
		if metrics.Pairs.PairCoverage == nil {
			return &MetricInvalid{Scope: scope + ".pairs", Field: "pair_coverage"}
		}
	}
	return nil
}

func validateBinaryMetrics(scope string, metrics BinaryMetrics, gates Gates) error {
	if metrics.ScoreCoverage == nil {
		return &MetricInvalid{Scope: scope, Field: "score_coverage"}
	}
	if metrics.UnavailableRate == nil {
		return &MetricInvalid{Scope: scope, Field: "unavailable_rate"}
	}
	if metrics.InconclusiveRate == nil {
		return &MetricInvalid{Scope: scope, Field: "inconclusive_rate"}
	}
	if metrics.GoldViolation > 0 && metrics.Recall == nil {
		return &MetricInvalid{Scope: scope, Field: "recall"}
	}
	if gates.MaxBrier != nil && metrics.Brier == nil {
		return &MetricInvalid{Scope: scope, Field: "brier"}
	}
	if gates.MaxECE != nil && metrics.Calibration.ECE == nil {
		return &MetricInvalid{Scope: scope, Field: "ece"}
	}
	return nil
}

func compareBinaryGates(regressions *[]Regression, scope, rule string, before, after BinaryMetrics, gates Gates) {
	if after.FalsePositives-before.FalsePositives > gates.MaxNewFalsePositives {
		*regressions = append(*regressions, countRegression(scope, rule, "new_false_positives", before.FalsePositives, after.FalsePositives, gates.MaxNewFalsePositives))
	}
	if after.FalseNegatives-before.FalseNegatives > gates.MaxNewMissedViolations {
		*regressions = append(*regressions, countRegression(scope, rule, "new_missed_violations", before.FalseNegatives, after.FalseNegatives, gates.MaxNewMissedViolations))
	}
	if below(after.ScoreCoverage, gates.MinScoreCoverage) {
		*regressions = append(*regressions, valueRegression(scope, rule, "score_coverage_below_gate", after.ScoreCoverage, floatPointer(gates.MinScoreCoverage)))
	}
	if above(after.UnavailableRate, gates.MaxUnavailableRate) {
		*regressions = append(*regressions, valueRegression(scope, rule, "unavailable_rate_above_gate", after.UnavailableRate, floatPointer(gates.MaxUnavailableRate)))
	}
	if above(after.InconclusiveRate, gates.MaxInconclusiveRate) {
		*regressions = append(*regressions, valueRegression(scope, rule, "inconclusive_rate_above_gate", after.InconclusiveRate, floatPointer(gates.MaxInconclusiveRate)))
	}
	if gates.MinRecall > 0 && after.GoldViolation > 0 && below(after.Recall, gates.MinRecall) {
		*regressions = append(*regressions, valueRegression(scope, rule, "recall_below_gate", after.Recall, floatPointer(gates.MinRecall)))
	}
	if drop(before.ScoreCoverage, after.ScoreCoverage) > gates.MaxScoreCoverageDrop {
		*regressions = append(*regressions, deltaRegression(scope, rule, "score_coverage_drop", before.ScoreCoverage, after.ScoreCoverage, gates.MaxScoreCoverageDrop))
	}
	if increase(before.InconclusiveRate, after.InconclusiveRate) > gates.MaxInconclusiveRateIncrease {
		*regressions = append(*regressions, deltaRegression(scope, rule, "inconclusive_rate_increase", before.InconclusiveRate, after.InconclusiveRate, gates.MaxInconclusiveRateIncrease))
	}
	if increase(before.UnavailableRate, after.UnavailableRate) > gates.MaxUnavailableRateIncrease {
		*regressions = append(*regressions, deltaRegression(scope, rule, "unavailable_rate_increase", before.UnavailableRate, after.UnavailableRate, gates.MaxUnavailableRateIncrease))
	}
	if increase(before.Brier, after.Brier) > gates.MaxBrierIncrease {
		*regressions = append(*regressions, deltaRegression(scope, rule, "brier_increase", before.Brier, after.Brier, gates.MaxBrierIncrease))
	}
	if gates.MaxBrier != nil && above(after.Brier, *gates.MaxBrier) {
		*regressions = append(*regressions, valueRegression(scope, rule, "brier_above_gate", after.Brier, gates.MaxBrier))
	}
	if gates.MaxECE != nil && above(after.Calibration.ECE, *gates.MaxECE) {
		*regressions = append(*regressions, valueRegression(scope, rule, "ece_above_gate", after.Calibration.ECE, gates.MaxECE))
	}
	if gates.MaxECEIncrease != nil && increase(before.Calibration.ECE, after.Calibration.ECE) > *gates.MaxECEIncrease {
		*regressions = append(*regressions, deltaRegression(scope, rule, "ece_increase", before.Calibration.ECE, after.Calibration.ECE, *gates.MaxECEIncrease))
	}
}

func compareRuleGates(regressions *[]Regression, before, after map[string]RuleMetrics, gates Gates) {
	codes := make([]string, 0, len(after))
	for code := range after {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		beforeRule, ok := before[code]
		if !ok {
			*regressions = append(*regressions, Regression{Scope: "rule", Rule: code, Kind: "missing_baseline_rule"})
			continue
		}
		afterRule := after[code]
		if beforeRule.Binary.Observations == 0 && afterRule.Binary.Observations == 0 {
			continue
		}
		compareBinaryGates(regressions, "rule", code, beforeRule.Binary, afterRule.Binary, gates)
	}
}

func compareCrossRules(regressions *[]Regression, before, after []CrossRuleActivation, gates Gates) {
	beforeSet := make(map[string]struct{})
	for _, activation := range before {
		if unexpectedActivations(activation) > 0 {
			beforeSet[activation.TargetRule+"\x00"+activation.ActivatedRule] = struct{}{}
		}
	}
	newActivations := 0
	for _, activation := range after {
		key := activation.TargetRule + "\x00" + activation.ActivatedRule
		if unexpectedActivations(activation) > 0 {
			if _, exists := beforeSet[key]; !exists {
				newActivations++
				*regressions = append(*regressions, Regression{Scope: "cross_rule", Rule: activation.TargetRule, Kind: "new_activation", Pair: activation.ActivatedRule})
			}
		}
	}
	if newActivations > gates.MaxNewActivations {
		*regressions = append(*regressions, countRegression("global", "", "new_cross_rule_activations", 0, newActivations, gates.MaxNewActivations))
	}
}

func unexpectedActivations(activation CrossRuleActivation) int {
	if activation.UnexpectedActivations == 0 && activation.AllowlistedActivations == 0 {
		return activation.Activations
	}
	return activation.UnexpectedActivations
}

func comparePairGates(regressions *[]Regression, before, after PairMetrics, gates Gates) {
	if after.Expected == 0 {
		return
	}
	if below(after.FlipRate, gates.MinFlipRate) {
		*regressions = append(*regressions, valueRegression("pairs", "", "flip_rate_below_gate", after.FlipRate, floatPointer(gates.MinFlipRate)))
	}
	if drop(before.FlipRate, after.FlipRate) > gates.MaxFlipRateDrop {
		*regressions = append(*regressions, deltaRegression("pairs", "", "flip_rate_drop", before.FlipRate, after.FlipRate, gates.MaxFlipRateDrop))
	}
	if below(after.PairCoverage, gates.MinScoreCoverage) {
		*regressions = append(*regressions, valueRegression("pairs", "", "pair_coverage_below_gate", after.PairCoverage, floatPointer(gates.MinScoreCoverage)))
	}
}

func countRegression(scope, rule, kind string, before, after, limit int) Regression {
	return Regression{Scope: scope, Rule: rule, Kind: kind, Baseline: floatPointer(float64(before)), Candidate: floatPointer(float64(after)), Limit: floatPointer(float64(limit))}
}

func valueRegression(scope, rule, kind string, candidate, limit *float64) Regression {
	return Regression{Scope: scope, Rule: rule, Kind: kind, Candidate: cloneFloat(candidate), Limit: cloneFloat(limit)}
}

func deltaRegression(scope, rule, kind string, before, after *float64, limit float64) Regression {
	return Regression{Scope: scope, Rule: rule, Kind: kind, Baseline: cloneFloat(before), Candidate: cloneFloat(after), Limit: floatPointer(limit)}
}

func below(value *float64, limit float64) bool {
	return value == nil || *value < limit
}

func above(value *float64, limit float64) bool {
	return value == nil || *value > limit
}

func increase(before, after *float64) float64 {
	if before == nil || after == nil {
		return 0
	}
	return *after - *before
}

func drop(before, after *float64) float64 {
	if before == nil || after == nil {
		return 0
	}
	return *before - *after
}

func floatPointer(value float64) *float64 { return &value }
