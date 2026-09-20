package harness

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func writeSummary(directory string, result RunResults) error {
	if result.Metrics == nil {
		return fmt.Errorf("metrics are required for summary")
	}
	var summary strings.Builder
	summary.WriteString("# Jeff evaluation summary\n\n")
	fmt.Fprintf(&summary, "- Dataset: `%s` (%s)\n", markdownValue(result.Metadata.DatasetName), markdownValue(result.Metadata.DatasetVersion))
	fmt.Fprintf(&summary, "- Split: `%s`\n- Provider: `%s`\n- Model: `%s`\n- Profile: `%s`\n- Repetitions: %d\n\n", markdownValue(result.Metadata.Split), markdownValue(result.Metadata.Provider), markdownValue(result.Metadata.Model), markdownValue(result.Metadata.Profile), result.Metadata.Repetitions)
	writeRegressions(&summary, "Gate failures", result.GateFailures)
	if result.Baseline != nil {
		writeRegressions(&summary, "Baseline regressions", result.Baseline.Regressions)
	}

	summary.WriteString("## Global metrics\n\n")
	writeMetricTable(&summary, result.Metrics.Global)

	summary.WriteString("## Per rule\n\n")
	rules := slices.Sorted(maps.Keys(result.Metrics.PerRule))
	for _, code := range rules {
		fmt.Fprintf(&summary, "### %s\n\n", markdownValue(code))
		writeMetricTable(&summary, result.Metrics.PerRule[code].Binary)
		writeCostTable(&summary, result.Metrics.PerRule[code].Cost)
	}

	summary.WriteString("## Cost\n\n")
	writeCostTable(&summary, result.Metrics.Cost)

	summary.WriteString("## Pairs\n\n")
	fmt.Fprintf(&summary, "- Expected: %d\n- Successful flips: %d\n- Flip rate: %s\n- Pair coverage: %s\n- Score order rate: %s\n- Mean score delta: %s\n\n", result.Metrics.Pairs.Expected, result.Metrics.Pairs.Successful, formatMetric(result.Metrics.Pairs.FlipRate), formatMetric(result.Metrics.Pairs.PairCoverage), formatMetric(result.Metrics.Pairs.OrderRate), formatMetric(result.Metrics.Pairs.MeanDelta))

	if len(result.Metrics.CrossRule) > 0 {
		summary.WriteString("## Cross-rule activations\n\n| Target | Activated | Count | Allowlisted | Unexpected | Opportunities | Rate |\n|---|---|---:|---:|---:|---:|---:|\n")
		for _, activation := range result.Metrics.CrossRule {
			fmt.Fprintf(&summary, "| %s | %s | %d | %d | %d | %d | %s |\n", markdownValue(activation.TargetRule), markdownValue(activation.ActivatedRule), activation.Activations, activation.AllowlistedActivations, activation.UnexpectedActivations, activation.Opportunities, formatMetric(activation.Rate))
		}
		summary.WriteString("\n")
	}

	summary.WriteString("## Latency\n\n")
	fmt.Fprintf(&summary, "- Observations: %d\n- Attempts: %d\n- p50: %s ms\n- p95: %s ms\n- p99: %s ms\n- Provider p50: %s ms\n- Provider p95: %s ms\n- Provider p99: %s ms\n\n", result.Metrics.Latency.Count, result.Metrics.Latency.Attempts, formatMetric(result.Metrics.Latency.P50MS), formatMetric(result.Metrics.Latency.P95MS), formatMetric(result.Metrics.Latency.P99MS), formatMetric(result.Metrics.Latency.ProviderP50MS), formatMetric(result.Metrics.Latency.ProviderP95MS), formatMetric(result.Metrics.Latency.ProviderP99MS))

	failures := make([]CaseResult, 0)
	for _, item := range result.Cases {
		if item.Unavailable || item.ErrorKind != "" {
			failures = append(failures, item)
		}
	}
	if len(failures) > 0 {
		summary.WriteString("## Unavailable/error cases\n\n| Case | Repetition | Kind | Jeff exit |\n|---|---:|---|---:|\n")
		for _, item := range failures {
			fmt.Fprintf(&summary, "| %s | %d | %s | %d |\n", markdownValue(item.CaseID), item.Repetition, markdownValue(item.ErrorKind), item.JeffExitCode)
		}
		summary.WriteString("\n")
	}

	data := []byte(summary.String())
	temporary := filepath.Join(directory, "summary.md.tmp")
	final := filepath.Join(directory, "summary.md")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish summary: %w", err)
	}
	return nil
}

func writeCostTable(summary *strings.Builder, metrics CostMetrics) {
	summary.WriteString("| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(summary, "| observations | %d |\n| usage observations | %d |\n| input tokens | %s |\n| output tokens | %s |\n| provider reported USD | %s |\n| calculated USD | %s |\n| cost USD | %s |\n| status | %s |\n\n", metrics.Observations, metrics.UsageObservations, formatInt64Metric(metrics.InputTokens), formatInt64Metric(metrics.OutputTokens), formatMetric(metrics.ProviderReportedUSD), formatMetric(metrics.CalculatedUSD), formatMetric(metrics.CostUSD), markdownValue(metrics.Status))
}

func writeRegressions(summary *strings.Builder, title string, regressions []Regression) {
	if len(regressions) == 0 {
		return
	}
	summary.WriteString("## " + title + "\n\n| Scope | Rule | Case | Pair | Kind | Candidate | Limit |\n|---|---|---|---|---|---:|---:|\n")
	for _, regression := range regressions {
		fmt.Fprintf(summary, "| %s | %s | %s | %s | %s | %s | %s |\n", markdownValue(regression.Scope), markdownValue(regression.Rule), markdownValue(regression.CaseID), markdownValue(regression.Pair), markdownValue(regression.Kind), formatMetric(regression.Candidate), formatMetric(regression.Limit))
	}
	summary.WriteString("\n")
}

func writeMetricTable(summary *strings.Builder, metrics BinaryMetrics) {
	summary.WriteString("| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(summary, "| observations | %d |\n| true positives | %d |\n| false positives | %d |\n| false negatives | %d |\n| precision | %s |\n| recall | %s |\n| F0.5 | %s |\n| false-positive rate | %s |\n| score coverage | %s |\n| decision coverage | %s |\n| inconclusive rate | %s |\n| unavailable rate | %s |\n| Brier | %s |\n| ECE | %s |\n\n", metrics.Observations, metrics.TruePositives, metrics.FalsePositives, metrics.FalseNegatives, formatMetric(metrics.Precision), formatMetric(metrics.Recall), formatMetric(metrics.F05), formatMetric(metrics.FalsePositiveRate), formatMetric(metrics.ScoreCoverage), formatMetric(metrics.DecisionCoverage), formatMetric(metrics.InconclusiveRate), formatMetric(metrics.UnavailableRate), formatMetric(metrics.Brier), formatMetric(metrics.Calibration.ECE))
}

func formatMetric(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%.6f", *value)
}

func formatInt64Metric(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(*value)
}

func markdownValue(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(value)
}
