package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"

	"jeff/internal/rules"
)

const ConfigSchemaVersion = 1

type Config struct {
	SchemaVersion int          `yaml:"schema_version"`
	Model         string       `yaml:"model"`
	Repetitions   int          `yaml:"repetitions"`
	Concurrency   int          `yaml:"concurrency"`
	Limits        Limits       `yaml:"limits"`
	Pricing       Pricing      `yaml:"pricing"`
	Thresholds    ThresholdSet `yaml:"thresholds"`
	Gates         Gates        `yaml:"gates"`
}

type Limits struct {
	ManifestBytes int64    `yaml:"manifest_bytes"`
	ConfigBytes   int64    `yaml:"config_bytes"`
	SourceBytes   int64    `yaml:"source_bytes"`
	Cases         int      `yaml:"cases"`
	RequestBytes  int64    `yaml:"request_bytes"`
	ResponseBytes int64    `yaml:"response_bytes"`
	StdoutBytes   int64    `yaml:"stdout_bytes"`
	StderrBytes   int64    `yaml:"stderr_bytes"`
	Timeouts      Timeouts `yaml:"timeouts"`
}

type Timeouts struct {
	SyntheticSeconds int `yaml:"synthetic_seconds"`
	RealSeconds      int `yaml:"real_seconds"`
}

type Pricing struct {
	InputUSDPerMillion  *float64 `yaml:"input_usd_per_million"`
	OutputUSDPerMillion *float64 `yaml:"output_usd_per_million"`
}

type ThresholdSet struct {
	Overrides map[string]Threshold `yaml:"overrides"`
}

type Threshold struct {
	PassBelow     *float64 `yaml:"pass_below"`
	FailAtOrAbove *float64 `yaml:"fail_at_or_above"`
}

type EffectiveThreshold struct {
	PassBelow     float64 `json:"pass_below"`
	FailAtOrAbove float64 `json:"fail_at_or_above"`
}

type Gates struct {
	MinCases                    int      `yaml:"min_cases"`
	MinScoreCoverage            float64  `yaml:"min_score_coverage"`
	MaxUnavailableRate          float64  `yaml:"max_unavailable_rate"`
	MaxInconclusiveRate         float64  `yaml:"max_inconclusive_rate"`
	MinRecall                   float64  `yaml:"min_recall"`
	MinFlipRate                 float64  `yaml:"min_flip_rate"`
	MaxBrier                    *float64 `yaml:"max_brier"`
	MaxECE                      *float64 `yaml:"max_ece"`
	MaxNewFalsePositives        int      `yaml:"max_new_false_positives"`
	MaxNewMissedViolations      int      `yaml:"max_new_missed_violations"`
	MaxNewActivations           int      `yaml:"max_new_activations"`
	MaxScoreCoverageDrop        float64  `yaml:"max_score_coverage_drop"`
	MaxFlipRateDrop             float64  `yaml:"max_flip_rate_drop"`
	MaxInconclusiveRateIncrease float64  `yaml:"max_inconclusive_rate_increase"`
	MaxUnavailableRateIncrease  float64  `yaml:"max_unavailable_rate_increase"`
	MaxBrierIncrease            float64  `yaml:"max_brier_increase"`
	MaxECEIncrease              *float64 `yaml:"max_ece_increase"`
	MaxP95LatencyIncreaseMS     *float64 `yaml:"max_p95_latency_increase_ms"`
	MaxCostIncreaseUSD          *float64 `yaml:"max_cost_increase_usd"`
}

var modelPattern = regexp.MustCompile(`^jev-[0-9]+\.[0-9]+\.[0-9]+$`)

func LoadConfig(filename string) (Config, error) {
	return LoadConfigWithOptions(filename, DatasetOptions{MaxManifestBytes: 64 << 10})
}

func LoadConfigWithOptions(filename string, options DatasetOptions) (Config, error) {
	if options.MaxManifestBytes <= 0 {
		options.MaxManifestBytes = 64 << 10
	}
	path, err := cleanRegularPath(filename)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	data, err := readBoundedFile(path, options.MaxManifestBytes)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", filename, err)
	}
	var config Config
	if err := decodeStrict(data, filename, &config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", filename, err)
	}
	catalog, err := rules.Load()
	if err != nil {
		return Config{}, fmt.Errorf("load catalog: %w", err)
	}
	if _, err := EffectiveThresholds(catalog, config.Thresholds.Overrides); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", filename, err)
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("schema_version must be %d", ConfigSchemaVersion)
	}
	if !modelPattern.MatchString(c.Model) {
		return fmt.Errorf("model must be a concrete Jev version")
	}
	if c.Repetitions < 1 || c.Repetitions > 10 {
		return fmt.Errorf("repetitions must be between 1 and 10")
	}
	if c.Concurrency < 1 || c.Concurrency > 16 {
		return fmt.Errorf("concurrency must be between 1 and 16")
	}
	if err := c.Limits.Validate(); err != nil {
		return err
	}
	for name, value := range map[string]*float64{"input_usd_per_million": c.Pricing.InputUSDPerMillion, "output_usd_per_million": c.Pricing.OutputUSDPerMillion} {
		if value != nil && !finiteNonNegative(*value) {
			return fmt.Errorf("pricing.%s must be finite and non-negative", name)
		}
	}
	if err := c.Gates.Validate(); err != nil {
		return err
	}
	for code, threshold := range c.Thresholds.Overrides {
		if threshold.PassBelow == nil || threshold.FailAtOrAbove == nil {
			return fmt.Errorf("threshold override %s must contain both bounds", code)
		}
		if err := validateBounds(*threshold.PassBelow, *threshold.FailAtOrAbove); err != nil {
			return fmt.Errorf("threshold override %s: %w", code, err)
		}
	}
	return nil
}

func (l Limits) Validate() error {
	for name, value := range map[string]int64{
		"manifest_bytes": l.ManifestBytes,
		"config_bytes":   l.ConfigBytes,
		"source_bytes":   l.SourceBytes,
		"request_bytes":  l.RequestBytes,
		"response_bytes": l.ResponseBytes,
		"stdout_bytes":   l.StdoutBytes,
		"stderr_bytes":   l.StderrBytes,
	} {
		if value <= 0 {
			return fmt.Errorf("limits.%s must be positive", name)
		}
	}
	if l.Cases < 1 || l.Cases > DefaultCaseCount {
		return fmt.Errorf("limits.cases must be between 1 and %d", DefaultCaseCount)
	}
	if l.Timeouts.SyntheticSeconds < 1 || l.Timeouts.RealSeconds < 1 {
		return fmt.Errorf("limits.timeouts must be positive")
	}
	return nil
}

func (g Gates) Validate() error {
	if g.MinCases < 1 {
		return fmt.Errorf("gates.min_cases must be positive")
	}
	for name, value := range map[string]float64{
		"min_score_coverage":             g.MinScoreCoverage,
		"max_unavailable_rate":           g.MaxUnavailableRate,
		"max_inconclusive_rate":          g.MaxInconclusiveRate,
		"min_recall":                     g.MinRecall,
		"min_flip_rate":                  g.MinFlipRate,
		"max_score_coverage_drop":        g.MaxScoreCoverageDrop,
		"max_flip_rate_drop":             g.MaxFlipRateDrop,
		"max_inconclusive_rate_increase": g.MaxInconclusiveRateIncrease,
		"max_unavailable_rate_increase":  g.MaxUnavailableRateIncrease,
		"max_brier_increase":             g.MaxBrierIncrease,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("gates.%s must be between 0 and 1", name)
		}
	}
	for name, value := range map[string]*float64{"max_brier": g.MaxBrier, "max_ece": g.MaxECE, "max_ece_increase": g.MaxECEIncrease} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1) {
			return fmt.Errorf("gates.%s must be null or between 0 and 1", name)
		}
	}
	for name, value := range map[string]*float64{"max_p95_latency_increase_ms": g.MaxP95LatencyIncreaseMS, "max_cost_increase_usd": g.MaxCostIncreaseUSD} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return fmt.Errorf("gates.%s must be null or non-negative", name)
		}
	}
	for name, value := range map[string]int{
		"max_new_false_positives":   g.MaxNewFalsePositives,
		"max_new_missed_violations": g.MaxNewMissedViolations,
		"max_new_activations":       g.MaxNewActivations,
	} {
		if value < 0 {
			return fmt.Errorf("gates.%s must not be negative", name)
		}
	}
	return nil
}

func HashConfig(config Config) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func EffectiveThresholds(catalog rules.Catalog, overrides map[string]Threshold) (map[string]EffectiveThreshold, error) {
	result := make(map[string]EffectiveThreshold, len(catalog.Rules))
	known := make(map[string]struct{}, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		if rule.Decision.PassBelow == nil || rule.Decision.FailAtOrAbove == nil {
			return nil, fmt.Errorf("catalog rule %s has incomplete decision bounds", rule.Code)
		}
		result[rule.Code] = EffectiveThreshold{PassBelow: *rule.Decision.PassBelow, FailAtOrAbove: *rule.Decision.FailAtOrAbove}
		known[rule.Code] = struct{}{}
	}
	for code, override := range overrides {
		if _, ok := known[code]; !ok {
			return nil, fmt.Errorf("threshold override references unknown rule %s", code)
		}
		if override.PassBelow == nil || override.FailAtOrAbove == nil {
			return nil, fmt.Errorf("threshold override %s must contain both bounds", code)
		}
		if err := validateBounds(*override.PassBelow, *override.FailAtOrAbove); err != nil {
			return nil, fmt.Errorf("threshold override %s: %w", code, err)
		}
		result[code] = EffectiveThreshold{PassBelow: *override.PassBelow, FailAtOrAbove: *override.FailAtOrAbove}
	}
	return result, nil
}

func HashEffectiveThresholds(thresholds map[string]EffectiveThreshold) (string, error) {
	codes := make([]string, 0, len(thresholds))
	for code := range thresholds {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	ordered := make([]struct {
		Code  string             `json:"code"`
		Value EffectiveThreshold `json:"threshold"`
	}, 0, len(codes))
	for _, code := range codes {
		ordered = append(ordered, struct {
			Code  string             `json:"code"`
			Value EffectiveThreshold `json:"threshold"`
		}{code, thresholds[code]})
	}
	data, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func validateBounds(passBelow, failAtOrAbove float64) error {
	if math.IsNaN(passBelow) || math.IsInf(passBelow, 0) || math.IsNaN(failAtOrAbove) || math.IsInf(failAtOrAbove, 0) || passBelow < 0 || failAtOrAbove > 1 || passBelow > failAtOrAbove {
		return fmt.Errorf("bounds must satisfy 0 <= pass_below <= fail_at_or_above <= 1")
	}
	return nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
