package harness

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"jeff/internal/rules"
)

type Metrics struct {
	Observations  int                    `json:"observations"`
	Ambiguous     int                    `json:"ambiguous"`
	NotApplicable int                    `json:"not_applicable"`
	Global        BinaryMetrics          `json:"global"`
	PerRule       map[string]RuleMetrics `json:"per_rule"`
	CrossRule     []CrossRuleActivation  `json:"cross_rule"`
	Pairs         PairMetrics            `json:"pairs"`
	Latency       LatencyMetrics         `json:"latency"`
	Cost          CostMetrics            `json:"cost"`
}

type RuleMetrics struct {
	Binary        BinaryMetrics `json:"binary"`
	Ambiguous     int           `json:"ambiguous"`
	NotApplicable int           `json:"not_applicable"`
	Cost          CostMetrics   `json:"cost"`
}

type BinaryMetrics struct {
	Observations      int         `json:"observations"`
	GoldClean         int         `json:"gold_clean"`
	GoldViolation     int         `json:"gold_violation"`
	TruePositives     int         `json:"true_positives"`
	FalsePositives    int         `json:"false_positives"`
	FalseNegatives    int         `json:"false_negatives"`
	Abstain           int         `json:"abstain"`
	Unavailable       int         `json:"unavailable"`
	Scored            int         `json:"scored"`
	Conclusive        int         `json:"conclusive"`
	Precision         *float64    `json:"precision"`
	Recall            *float64    `json:"recall"`
	F05               *float64    `json:"f0_5"`
	FalsePositiveRate *float64    `json:"false_positive_rate"`
	ScoreCoverage     *float64    `json:"score_coverage"`
	DecisionCoverage  *float64    `json:"decision_coverage"`
	InconclusiveRate  *float64    `json:"inconclusive_rate"`
	UnavailableRate   *float64    `json:"unavailable_rate"`
	Brier             *float64    `json:"brier"`
	Calibration       Calibration `json:"calibration"`
}

type Calibration struct {
	Scored int              `json:"scored"`
	Bins   []CalibrationBin `json:"bins"`
	ECE    *float64         `json:"ece"`
}

type CalibrationBin struct {
	Index      int      `json:"index"`
	Count      int      `json:"count"`
	ScoreMean  *float64 `json:"score_mean"`
	Prevalence *float64 `json:"prevalence"`
}

type CrossRuleActivation struct {
	TargetRule             string   `json:"target_rule"`
	ActivatedRule          string   `json:"activated_rule"`
	Activations            int      `json:"activations"`
	AllowlistedActivations int      `json:"allowlisted_activations"`
	UnexpectedActivations  int      `json:"unexpected_activations"`
	Opportunities          int      `json:"opportunities"`
	Rate                   *float64 `json:"rate"`
}

type PairMetrics struct {
	Expected     int          `json:"expected"`
	Successful   int          `json:"successful"`
	Covered      int          `json:"covered"`
	Ordered      int          `json:"ordered"`
	FlipRate     *float64     `json:"flip_rate"`
	PairCoverage *float64     `json:"pair_coverage"`
	OrderRate    *float64     `json:"order_rate"`
	MeanDelta    *float64     `json:"mean_delta"`
	ByPair       []PairResult `json:"by_pair"`
}

type PairResult struct {
	Pair         string   `json:"pair"`
	Rule         string   `json:"rule"`
	Expected     int      `json:"expected"`
	Successful   int      `json:"successful"`
	Covered      int      `json:"covered"`
	Ordered      int      `json:"ordered"`
	FlipRate     *float64 `json:"flip_rate"`
	PairCoverage *float64 `json:"pair_coverage"`
	OrderRate    *float64 `json:"order_rate"`
	MeanDelta    *float64 `json:"mean_delta"`
}

type LatencyMetrics struct {
	Count         int      `json:"count"`
	Attempts      int      `json:"attempts"`
	P50MS         *float64 `json:"p50_ms"`
	P95MS         *float64 `json:"p95_ms"`
	P99MS         *float64 `json:"p99_ms"`
	ProviderP50MS *float64 `json:"provider_p50_ms"`
	ProviderP95MS *float64 `json:"provider_p95_ms"`
	ProviderP99MS *float64 `json:"provider_p99_ms"`
}

type CostMetrics struct {
	Observations        int      `json:"observations"`
	UsageObservations   int      `json:"usage_observations"`
	InputTokens         *int64   `json:"input_tokens"`
	OutputTokens        *int64   `json:"output_tokens"`
	ProviderReportedUSD *float64 `json:"provider_reported_usd"`
	CalculatedUSD       *float64 `json:"calculated_usd"`
	CostUSD             *float64 `json:"cost_usd"`
	Status              string   `json:"status"`
}

type metricAccumulator struct {
	binary      BinaryMetrics
	brierSum    float64
	calibration calibrationAccumulator
}

type calibrationAccumulator struct {
	count    [10]int
	scoreSum [10]float64
	goldSum  [10]float64
	scored   int
}

type pairSide struct {
	clean     *CaseResult
	violation *CaseResult
}

type crossAccumulator struct {
	targetRule    string
	activatedRule string
	activations   int
	allowlisted   int
	unexpected    int
	opportunities int
}

func ComputeMetrics(observations []CaseResult, catalog rules.Catalog) (Metrics, error) {
	knownRules := make(map[string]struct{}, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		knownRules[rule.Code] = struct{}{}
	}
	global := metricAccumulator{}
	perRule := make(map[string]*metricAccumulator)
	perRuleLabels := make(map[string]*RuleMetrics)
	costByRule := make(map[string][]CaseResult)
	cross := make(map[string]*crossAccumulator)
	pairs := make(map[string]map[int]*pairSide)
	pairRules := make(map[string]string)
	latencies := make([]int64, 0, len(observations))
	providerLatencies := make([]int64, 0, len(observations))
	metrics := Metrics{Observations: len(observations), PerRule: make(map[string]RuleMetrics)}

	for index := range observations {
		observation := observations[index]
		if _, ok := knownRules[observation.Rule]; !ok {
			return Metrics{}, fmt.Errorf("observation %q references unknown rule %q", observation.CaseID, observation.Rule)
		}
		if observation.LatencyMS >= 0 {
			latencies = append(latencies, observation.LatencyMS)
		}
		if observation.ProviderLatencyMS >= 0 {
			providerLatencies = append(providerLatencies, observation.ProviderLatencyMS)
		}
		if observation.Attempts >= 0 {
			metrics.Latency.Attempts += observation.Attempts
		}
		if _, ok := perRule[observation.Rule]; !ok {
			perRule[observation.Rule] = &metricAccumulator{}
			perRuleLabels[observation.Rule] = &RuleMetrics{}
		}
		costByRule[observation.Rule] = append(costByRule[observation.Rule], observation)
		switch observation.Label {
		case LabelClean, LabelViolation:
			recordBinary(&global, observation)
			recordBinary(perRule[observation.Rule], observation)
		case LabelAmbiguous:
			metrics.Ambiguous++
			perRuleLabels[observation.Rule].Ambiguous++
		case LabelNotApplicable:
			metrics.NotApplicable++
			perRuleLabels[observation.Rule].NotApplicable++
		default:
			return Metrics{}, fmt.Errorf("observation %q has invalid label %q", observation.CaseID, observation.Label)
		}

		if observation.ProcessValid && !observation.Unavailable {
			seenChecks := make(map[string]ObservedCheck, len(observation.Checks))
			for _, check := range observation.Checks {
				if _, exists := seenChecks[check.Code]; exists {
					return Metrics{}, fmt.Errorf("observation %q has duplicate check %s", observation.CaseID, check.Code)
				}
				seenChecks[check.Code] = check
			}
			for _, rule := range catalog.Rules {
				if rule.Code == observation.Rule {
					continue
				}
				check, exists := seenChecks[rule.Code]
				if !exists {
					continue
				}
				key := observation.Rule + "\x00" + rule.Code
				entry := cross[key]
				if entry == nil {
					entry = &crossAccumulator{targetRule: observation.Rule, activatedRule: rule.Code}
					cross[key] = entry
				}
				entry.opportunities++
				if check.Status == "violation" {
					entry.activations++
					if slices.Contains(observation.AllowedActivations, rule.Code) {
						entry.allowlisted++
					} else {
						entry.unexpected++
					}
				}
			}
		}

		if observation.Pair != "" && (observation.Label == LabelClean || observation.Label == LabelViolation) {
			if existing, ok := pairRules[observation.Pair]; ok && existing != observation.Rule {
				return Metrics{}, fmt.Errorf("pair %q targets rules %s and %s", observation.Pair, existing, observation.Rule)
			}
			pairRules[observation.Pair] = observation.Rule
			if pairs[observation.Pair] == nil {
				pairs[observation.Pair] = make(map[int]*pairSide)
			}
			side := pairs[observation.Pair][observation.Repetition]
			if side == nil {
				side = &pairSide{}
				pairs[observation.Pair][observation.Repetition] = side
			}
			if observation.Label == LabelClean {
				if side.clean != nil {
					return Metrics{}, fmt.Errorf("pair %q repetition %d has duplicate clean case", observation.Pair, observation.Repetition)
				}
				side.clean = &observations[index]
			} else {
				if side.violation != nil {
					return Metrics{}, fmt.Errorf("pair %q repetition %d has duplicate violation case", observation.Pair, observation.Repetition)
				}
				side.violation = &observations[index]
			}
		}
	}

	metrics.Global = finalizeAccumulator(&global)
	for code, accumulator := range perRule {
		labels := perRuleLabels[code]
		labels.Binary = finalizeAccumulator(accumulator)
		labels.Cost = finalizeCost(costByRule[code])
		metrics.PerRule[code] = *labels
	}
	metrics.Cost = finalizeCost(observations)
	metrics.CrossRule = finalizeCrossRules(cross)
	metrics.Pairs = finalizePairs(pairs, pairRules)
	metrics.Latency.Count = len(latencies)
	metrics.Latency.P50MS = percentile(latencies, 0.50)
	metrics.Latency.P95MS = percentile(latencies, 0.95)
	metrics.Latency.P99MS = percentile(latencies, 0.99)
	metrics.Latency.ProviderP50MS = percentile(providerLatencies, 0.50)
	metrics.Latency.ProviderP95MS = percentile(providerLatencies, 0.95)
	metrics.Latency.ProviderP99MS = percentile(providerLatencies, 0.99)
	return metrics, nil
}

func recordBinary(accumulator *metricAccumulator, observation CaseResult) {
	binary := &accumulator.binary
	binary.Observations++
	if observation.Label == LabelClean {
		binary.GoldClean++
	} else {
		binary.GoldViolation++
	}
	if observation.Unavailable || !observation.ProcessValid {
		binary.Unavailable++
		return
	}
	status := observation.EvalStatus
	if status != "pass" && status != "violation" && status != "inconclusive" {
		binary.Unavailable++
		return
	}
	if observation.TargetNoul != nil && validScore(*observation.TargetNoul) {
		binary.Scored++
		gold := 0.0
		if observation.Label == LabelViolation {
			gold = 1
		}
		accumulator.brierSum += (*observation.TargetNoul - gold) * (*observation.TargetNoul - gold)
		accumulator.calibration.add(*observation.TargetNoul, gold)
	}
	switch status {
	case "pass", "violation":
		binary.Conclusive++
	case "inconclusive":
		binary.Abstain++
	}
	if observation.Label == LabelViolation && status == "violation" {
		binary.TruePositives++
	}
	if observation.Label == LabelClean && status == "violation" {
		binary.FalsePositives++
	}
}

func finalizeAccumulator(accumulator *metricAccumulator) BinaryMetrics {
	binary := accumulator.binary
	binary.FalseNegatives = binary.GoldViolation - binary.TruePositives
	binary.Precision = ratio(binary.TruePositives, binary.TruePositives+binary.FalsePositives)
	binary.Recall = ratio(binary.TruePositives, binary.GoldViolation)
	f05Denominator := 1.25*float64(binary.TruePositives) + 0.25*float64(binary.FalseNegatives) + float64(binary.FalsePositives)
	if f05Denominator > 0 {
		value := 1.25 * float64(binary.TruePositives) / f05Denominator
		binary.F05 = &value
	}
	binary.FalsePositiveRate = ratio(binary.FalsePositives, binary.GoldClean)
	binary.ScoreCoverage = ratio(binary.Scored, binary.Observations)
	binary.DecisionCoverage = ratio(binary.Conclusive, binary.Observations)
	binary.InconclusiveRate = ratio(binary.Abstain, binary.Observations)
	binary.UnavailableRate = ratio(binary.Unavailable, binary.Observations)
	if binary.Scored > 0 {
		value := accumulator.brierSum / float64(binary.Scored)
		binary.Brier = &value
	}
	binary.Calibration = accumulator.calibration.finalize()
	return binary
}

func (c *calibrationAccumulator) add(score, gold float64) {
	index := int(score * 10)
	if index >= len(c.count) {
		index = len(c.count) - 1
	}
	c.count[index]++
	c.scoreSum[index] += score
	c.goldSum[index] += gold
	c.scored++
}

func (c calibrationAccumulator) finalize() Calibration {
	result := Calibration{Scored: c.scored, Bins: make([]CalibrationBin, len(c.count))}
	weightedError := 0.0
	for index := range c.count {
		bin := CalibrationBin{Index: index, Count: c.count[index]}
		if bin.Count > 0 {
			scoreMean := c.scoreSum[index] / float64(bin.Count)
			prevalence := c.goldSum[index] / float64(bin.Count)
			bin.ScoreMean = &scoreMean
			bin.Prevalence = &prevalence
			weightedError += float64(bin.Count) * math.Abs(scoreMean-prevalence)
		}
		result.Bins[index] = bin
	}
	if c.scored > 0 {
		ece := weightedError / float64(c.scored)
		result.ECE = &ece
	}
	return result
}

func finalizeCrossRules(values map[string]*crossAccumulator) []CrossRuleActivation {
	result := make([]CrossRuleActivation, 0, len(values))
	for _, value := range values {
		item := CrossRuleActivation{TargetRule: value.targetRule, ActivatedRule: value.activatedRule, Activations: value.activations, AllowlistedActivations: value.allowlisted, UnexpectedActivations: value.unexpected, Opportunities: value.opportunities, Rate: ratio(value.activations, value.opportunities)}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TargetRule != result[j].TargetRule {
			return result[i].TargetRule < result[j].TargetRule
		}
		return result[i].ActivatedRule < result[j].ActivatedRule
	})
	return result
}

func finalizePairs(groups map[string]map[int]*pairSide, pairRules map[string]string) PairMetrics {
	result := PairMetrics{ByPair: make([]PairResult, 0, len(groups))}
	totalDelta := 0.0
	for pair, repetitions := range groups {
		item := PairResult{Pair: pair, Rule: pairRules[pair], Expected: len(repetitions)}
		deltaSum := 0.0
		for _, side := range repetitions {
			if side.clean == nil || side.violation == nil {
				continue
			}
			if side.clean.EvalStatus == "pass" && side.violation.EvalStatus == "violation" {
				item.Successful++
			}
			if validObservationScore(side.clean) && validObservationScore(side.violation) {
				item.Covered++
				delta := *side.violation.TargetNoul - *side.clean.TargetNoul
				deltaSum += delta
				if delta > 0 {
					item.Ordered++
				}
			}
		}
		item.FlipRate = ratio(item.Successful, item.Expected)
		item.PairCoverage = ratio(item.Covered, item.Expected)
		item.OrderRate = ratio(item.Ordered, item.Covered)
		if item.Covered > 0 {
			mean := deltaSum / float64(item.Covered)
			item.MeanDelta = &mean
			totalDelta += deltaSum
		}
		result.ByPair = append(result.ByPair, item)
		result.Expected += item.Expected
		result.Successful += item.Successful
		result.Covered += item.Covered
		result.Ordered += item.Ordered
	}
	sort.Slice(result.ByPair, func(i, j int) bool { return result.ByPair[i].Pair < result.ByPair[j].Pair })
	result.FlipRate = ratio(result.Successful, result.Expected)
	result.PairCoverage = ratio(result.Covered, result.Expected)
	result.OrderRate = ratio(result.Ordered, result.Covered)
	if result.Covered > 0 {
		mean := totalDelta / float64(result.Covered)
		result.MeanDelta = &mean
	}
	return result
}

func finalizeCost(observations []CaseResult) CostMetrics {
	requested := make([]CaseResult, 0, len(observations))
	for _, observation := range observations {
		if observation.Attempts > 0 {
			requested = append(requested, observation)
		}
	}
	result := CostMetrics{Observations: len(requested)}
	if len(requested) == 0 {
		result.Status = "no_requests"
		return result
	}
	var inputTokens, outputTokens int64
	var providerReported, calculated, cost float64
	usageComplete, costComplete := true, true
	providerCount := 0
	for _, observation := range requested {
		if observation.InputTokens == nil || observation.OutputTokens == nil {
			usageComplete = false
		} else {
			result.UsageObservations++
			inputTokens += *observation.InputTokens
			outputTokens += *observation.OutputTokens
		}
		if observation.ProviderCostUSD != nil {
			providerCount++
			providerReported += *observation.ProviderCostUSD
		}
		if observation.CostUSD == nil || observation.CalculatedCostUSD == nil {
			costComplete = false
		} else {
			calculated += *observation.CalculatedCostUSD
			cost += *observation.CostUSD
		}
	}
	if usageComplete {
		result.InputTokens = &inputTokens
		result.OutputTokens = &outputTokens
	}
	if providerCount > 0 {
		result.ProviderReportedUSD = &providerReported
	}
	if costComplete {
		result.CalculatedUSD = &calculated
		result.CostUSD = &cost
		result.Status = "calculated"
	} else if !usageComplete {
		result.Status = "unknown_usage"
	} else {
		result.Status = "unknown_pricing"
	}
	return result
}

func validObservationScore(observation *CaseResult) bool {
	return observation != nil && observation.ProcessValid && !observation.Unavailable && observation.TargetNoul != nil && validScore(*observation.TargetNoul)
}

func validScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func ratio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

func percentile(values []int64, quantile float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := int(math.Ceil(quantile*float64(len(copyValues)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copyValues) {
		index = len(copyValues) - 1
	}
	value := float64(copyValues[index])
	return &value
}
