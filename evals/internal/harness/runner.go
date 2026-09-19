package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"jeff/internal/credentials"
	"jeff/internal/rules"
)

const ResultsSchemaVersion = 1

type RunOptions struct {
	Dataset    Dataset
	Config     Config
	JeffBinary string
	RepoRoot   string
	Profile    string
	Provider   string
	OutputDir  string
}

type RunResults struct {
	SchemaVersion  int                 `json:"schema_version"`
	MetricsVersion int                 `json:"metrics_version"`
	Metadata       RunMetadata         `json:"metadata"`
	Cases          []CaseResult        `json:"cases"`
	Metrics        *Metrics            `json:"metrics,omitempty"`
	GateFailures   []Regression        `json:"gate_failures,omitempty"`
	Baseline       *BaselineComparison `json:"baseline,omitempty"`
}

type RunMetadata struct {
	DatasetHash            string `json:"dataset_hash"`
	DatasetName            string `json:"dataset_name"`
	DatasetVersion         string `json:"dataset_version"`
	Split                  string `json:"split"`
	SelectionHash          string `json:"selection_hash"`
	Profile                string `json:"profile"`
	Provider               string `json:"provider"`
	Model                  string `json:"model"`
	Repetitions            int    `json:"repetitions"`
	BinarySHA256           string `json:"binary_sha256"`
	CatalogHash            string `json:"catalog_hash"`
	ConfigHash             string `json:"config_hash"`
	EffectiveThresholdHash string `json:"effective_threshold_hash"`
	Concurrency            int    `json:"concurrency"`
	GitCommit              string `json:"git_commit,omitempty"`
	WorktreeDirty          bool   `json:"worktree_dirty"`
}

type CaseResult struct {
	CaseID             string          `json:"case_id"`
	Rule               string          `json:"rule"`
	Language           string          `json:"language"`
	Label              Label           `json:"label"`
	Difficulty         Difficulty      `json:"difficulty"`
	Pair               string          `json:"pair,omitempty"`
	AllowedActivations []string        `json:"allowed_activations,omitempty"`
	Filename           string          `json:"filename"`
	SourceSHA256       string          `json:"source_sha256"`
	Repetition         int             `json:"repetition"`
	JeffExitCode       int             `json:"jeff_exit_code"`
	JeffStatus         string          `json:"jeff_status,omitempty"`
	EvalStatus         string          `json:"eval_status,omitempty"`
	TargetStatus       string          `json:"target_status,omitempty"`
	TargetNoul         *float64        `json:"target_noul,omitempty"`
	Checks             []ObservedCheck `json:"checks"`
	Errors             []ObservedError `json:"errors"`
	Warnings           int             `json:"warnings"`
	ProcessValid       bool            `json:"process_valid"`
	Unavailable        bool            `json:"unavailable"`
	ErrorKind          string          `json:"error_kind,omitempty"`
	StdoutBytes        int             `json:"stdout_bytes"`
	StderrBytes        int             `json:"stderr_bytes"`
	Attempts           int             `json:"attempts"`
	LatencyMS          int64           `json:"latency_ms"`
	ProviderLatencyMS  int64           `json:"provider_latency_ms"`
	UsageSource        string          `json:"usage_source"`
	InputTokens        *int64          `json:"input_tokens"`
	OutputTokens       *int64          `json:"output_tokens"`
	ProviderCostUSD    *float64        `json:"provider_cost_usd"`
	CalculatedCostUSD  *float64        `json:"calculated_cost_usd"`
	CostUSD            *float64        `json:"cost_usd"`
	CostStatus         string          `json:"cost_status"`
}

type ObservedCheck struct {
	Path   string   `json:"path"`
	Code   string   `json:"code"`
	Status string   `json:"status"`
	Noul   *float64 `json:"noul,omitempty"`
}

type ObservedError struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	Code       string `json:"code,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type cliResult struct {
	SchemaVersion int          `json:"schema_version"`
	Checks        []cliCheck   `json:"checks"`
	Errors        []cliError   `json:"errors"`
	Warnings      []cliWarning `json:"warnings"`
}

type cliCheck struct {
	Path    string   `json:"path"`
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Message string   `json:"message"`
	Status  string   `json:"status"`
	Noul    *float64 `json:"noul,omitempty"`
}

type cliError struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type cliWarning struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func Run(ctx context.Context, options RunOptions) (RunResults, error) {
	if options.Provider == "" {
		options.Provider = "synthetic"
	}
	if options.Provider != "synthetic" && options.Provider != "real" {
		return RunResults{}, fmt.Errorf("provider %q is not available", options.Provider)
	}
	if options.Provider == "real" && len(options.Config.Thresholds.Overrides) > 0 {
		return RunResults{}, fmt.Errorf("threshold overrides are not allowed for real provider runs")
	}
	if options.OutputDir == "" {
		return RunResults{}, fmt.Errorf("output directory is required")
	}
	if err := options.Config.Validate(); err != nil {
		return RunResults{}, err
	}
	if options.RepoRoot == "" {
		options.RepoRoot = findRepoRoot()
	}
	catalog, err := rules.LoadWithOptions(rules.LoadOptions{Model: options.Config.Model})
	if err != nil {
		return RunResults{}, fmt.Errorf("load catalog: %w", err)
	}
	effectiveThresholds, err := EffectiveThresholds(catalog, options.Config.Thresholds.Overrides)
	if err != nil {
		return RunResults{}, err
	}
	thresholdHash, err := HashEffectiveThresholds(effectiveThresholds)
	if err != nil {
		return RunResults{}, fmt.Errorf("hash effective thresholds: %w", err)
	}
	configHash, err := HashConfig(options.Config)
	if err != nil {
		return RunResults{}, fmt.Errorf("hash config: %w", err)
	}
	selected, err := selectCases(options.Dataset, options.Profile)
	if err != nil {
		return RunResults{}, err
	}
	if err := prepareOutputDir(options.OutputDir); err != nil {
		return RunResults{}, err
	}
	binary, cleanup, err := resolveJeffBinary(ctx, options)
	if err != nil {
		return RunResults{}, err
	}
	defer cleanup()
	binaryHash, err := hashFile(binary)
	if err != nil {
		return RunResults{}, fmt.Errorf("hash Jeff binary: %w", err)
	}
	catalogHash, err := hashCatalog(catalog)
	if err != nil {
		return RunResults{}, fmt.Errorf("hash catalog: %w", err)
	}
	gitCommit, dirty := gitMetadata(options.RepoRoot)
	selectionHash, err := hashSelection(selected, options)
	if err != nil {
		return RunResults{}, fmt.Errorf("hash selection: %w", err)
	}
	var provider providerMeter
	apiKey := ""
	if options.Provider == "synthetic" {
		provider, err = NewSyntheticProvider(catalog, options.Config.Model, options.Dataset)
	} else {
		apiKey, err = credentials.LoadAPIKey()
		if err != nil {
			return RunResults{}, err
		}
		if err := validateRealBaseURL(os.Getenv("TYPESAFE_BASE_URL")); err != nil {
			return RunResults{}, err
		}
		provider, err = NewMeterProxy(os.Getenv("TYPESAFE_BASE_URL"), options.Config.Limits.RequestBytes, options.Config.Limits.ResponseBytes)
	}
	if err != nil {
		return RunResults{}, err
	}
	defer provider.Close()

	result := RunResults{
		SchemaVersion:  ResultsSchemaVersion,
		MetricsVersion: 1,
		Metadata: RunMetadata{
			DatasetHash:            options.Dataset.Hash,
			DatasetName:            options.Dataset.Manifest.Dataset.Name,
			DatasetVersion:         options.Dataset.Manifest.Dataset.Version,
			Split:                  options.Dataset.Manifest.Dataset.Split,
			SelectionHash:          selectionHash,
			Profile:                profileName(options.Profile),
			Provider:               options.Provider,
			Model:                  options.Config.Model,
			Repetitions:            options.Config.Repetitions,
			BinarySHA256:           binaryHash,
			CatalogHash:            catalogHash,
			ConfigHash:             configHash,
			EffectiveThresholdHash: thresholdHash,
			Concurrency:            options.Config.Concurrency,
			GitCommit:              gitCommit,
			WorktreeDirty:          dirty,
		},
		Cases: make([]CaseResult, 0, len(selected)*options.Config.Repetitions),
	}
	for _, item := range selected {
		for repetition := 1; repetition <= options.Config.Repetitions; repetition++ {
			if err := ctx.Err(); err != nil {
				return RunResults{}, err
			}
			before := provider.Snapshot()
			timeout := time.Duration(options.Config.Limits.Timeouts.SyntheticSeconds) * time.Second
			if options.Provider == "real" {
				timeout = time.Duration(options.Config.Limits.Timeouts.RealSeconds) * time.Second
			}
			caseContext, cancel := requestContext(ctx, timeout)
			observation := executeCase(caseContext, binary, provider.URL(), options.Config, catalog, effectiveThresholds, item, repetition, options.Provider, apiKey)
			cancel()
			applyMetering(&observation, meterDelta(before, provider.Snapshot()), options.Config.Pricing)
			result.Cases = append(result.Cases, observation)
		}
	}
	metrics, err := ComputeMetrics(result.Cases, catalog)
	if err != nil {
		return RunResults{}, err
	}
	result.Metrics = &metrics
	gateFailures, err := EvaluateGates(metrics, options.Config.Gates)
	if err != nil {
		return RunResults{}, err
	}
	result.GateFailures = gateFailures
	if err := WriteArtifacts(options.OutputDir, result); err != nil {
		return RunResults{}, err
	}
	return result, nil
}

func (r RunResults) ExitCode() int {
	qualityFailure := false
	for _, item := range r.Cases {
		if item.Unavailable || !item.ProcessValid {
			return 2
		}
		status := evaluatedStatus(item)
		switch item.Label {
		case LabelClean:
			if status != "pass" {
				qualityFailure = true
			}
		case LabelViolation:
			if status != "violation" {
				qualityFailure = true
			}
		case LabelAmbiguous:
			if status != "inconclusive" {
				qualityFailure = true
			}
		case LabelNotApplicable:
			if status != "" {
				qualityFailure = true
			}
		}
	}
	if qualityFailure || len(r.GateFailures) > 0 {
		return 1
	}
	return 0
}

func selectCases(dataset Dataset, profile string) ([]LoadedCase, error) {
	profile = profileName(profile)
	selected := make([]LoadedCase, 0, len(dataset.Cases))
	for _, item := range dataset.Cases {
		if profile == "smoke" && !hasTag(item.Tags, "smoke") {
			continue
		}
		selected = append(selected, item)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("profile %q selected no cases", profile)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected, nil
}

func profileName(profile string) string {
	if profile == "" {
		return "all"
	}
	return profile
}

func hasTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if tag == wanted {
			return true
		}
	}
	return false
}

func executeCase(ctx context.Context, binary, providerURL string, config Config, catalog rules.Catalog, thresholds map[string]EffectiveThreshold, item LoadedCase, repetition int, provider, apiKey string) CaseResult {
	allowedActivations := append([]string(nil), item.AllowActivations...)
	sort.Strings(allowedActivations)
	result := CaseResult{
		CaseID: item.ID, Rule: item.Rule, Language: item.Language, Label: item.Label, Difficulty: item.Difficulty,
		Pair: item.Pair, AllowedActivations: allowedActivations, Filename: item.Source.Filename, SourceSHA256: item.SourceSHA256, Repetition: repetition,
		Checks: []ObservedCheck{}, Errors: []ObservedError{}, ProcessValid: false, LatencyMS: -1, ProviderLatencyMS: -1,
	}
	started := time.Now()
	workspace, err := os.MkdirTemp("", "jeff-eval-case-")
	if err != nil {
		result.Unavailable = true
		result.ErrorKind = "workspace"
		return result
	}
	defer os.RemoveAll(workspace)
	if _, err := MaterializeCase(workspace, item); err != nil {
		result.Unavailable = true
		result.ErrorKind = "materialize"
		return result
	}
	configPath := filepath.Join(workspace, "jeff.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("jev-version = %q\noutput-format = \"json\"\n", config.Model)), 0o600); err != nil {
		result.Unavailable = true
		result.ErrorKind = "config"
		return result
	}
	command := exec.CommandContext(ctx, binary, "check", "--config", configPath, "--no-cache", "--output-format", "json", "--", item.Source.Filename)
	command.Dir = workspace
	command.Env = providerEnvironment(providerURL, provider, apiKey)
	var stdout, stderr cappedBuffer
	stdout.limit = int(config.Limits.StdoutBytes)
	stderr.limit = int(config.Limits.StderrBytes)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	result.StdoutBytes = stdout.Len()
	result.StderrBytes = stderr.Len()
	result.LatencyMS = time.Since(started).Milliseconds()
	if stdout.exceeded || stderr.exceeded {
		result.Unavailable = true
		result.ErrorKind = "output_limit"
		return result
	}
	if command.ProcessState != nil {
		result.JeffExitCode = command.ProcessState.ExitCode()
	}
	var parsed cliResult
	if len(stdout.Bytes()) == 0 {
		result.Unavailable = true
		result.ErrorKind = processErrorKind(ctx, err)
		return result
	}
	if parseErr := decodeCLIResult(stdout.Bytes(), &parsed); parseErr != nil {
		result.Unavailable = true
		result.ErrorKind = "invalid_json"
		return result
	}
	if validateErr := validateCLIResult(parsed, result.Filename, result.Rule, catalog); validateErr != nil {
		result.Unavailable = true
		result.ErrorKind = "invalid_result"
		return result
	}
	expectedExit := cliExitCode(parsed)
	if result.JeffExitCode != expectedExit {
		result.Unavailable = true
		result.ErrorKind = "exit_mismatch"
		return result
	}
	result.ProcessValid = true
	result.Warnings = len(parsed.Warnings)
	for _, check := range parsed.Checks {
		noul := cloneFloat(check.Noul)
		result.Checks = append(result.Checks, ObservedCheck{Path: check.Path, Code: check.Code, Status: check.Status, Noul: noul})
		if check.Code == result.Rule {
			result.JeffStatus = check.Status
			result.TargetNoul = noul
		}
	}
	if result.TargetNoul != nil {
		result.EvalStatus = evalStatusForScore(result.Rule, *result.TargetNoul, thresholds)
		result.TargetStatus = result.EvalStatus
		if _, overridden := config.Thresholds.Overrides[result.Rule]; !overridden && result.EvalStatus != result.JeffStatus {
			result.Unavailable = true
			result.ErrorKind = "threshold_mismatch"
		}
	}
	for _, runErr := range parsed.Errors {
		result.Errors = append(result.Errors, ObservedError{Kind: runErr.Kind, Path: runErr.Path, Code: runErr.Code, HTTPStatus: runErr.HTTPStatus})
	}
	if len(result.Errors) > 0 {
		result.Unavailable = true
		result.ErrorKind = "provider_or_cli"
	}
	return result
}

func syntheticEnvironment(providerURL string) []string {
	return providerEnvironment(providerURL, "synthetic", syntheticAPIKey)
}

func providerEnvironment(providerURL, provider, apiKey string) []string {
	blocked := map[string]bool{
		"TYPESAFE_API_KEY": true, "TYPESAFE_BASE_URL": true, "JEFF_CACHE_DIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
	}
	environment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && !blocked[name] {
			environment = append(environment, entry)
		}
	}
	if provider == "synthetic" {
		apiKey = syntheticAPIKey
	}
	environment = append(environment,
		"TYPESAFE_API_KEY="+apiKey,
		"TYPESAFE_BASE_URL="+providerURL,
		"NO_PROXY=*")
	return environment
}

func decodeCLIResult(data []byte, result *cliResult) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON documents")
		}
		return err
	}
	if result.SchemaVersion != 1 {
		return fmt.Errorf("unsupported Jeff result schema %d", result.SchemaVersion)
	}
	return nil
}

func validateCLIResult(result cliResult, filename, target string, catalog rules.Catalog) error {
	expected := make(map[string]struct{})
	known := make(map[string]struct{}, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		known[rule.Code] = struct{}{}
		if rule.Applies(filename) {
			expected[rule.Code] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(result.Checks))
	for _, check := range result.Checks {
		if check.Path != filename {
			return fmt.Errorf("unexpected check path")
		}
		if _, ok := known[check.Code]; !ok {
			return fmt.Errorf("unknown check code")
		}
		if _, ok := expected[check.Code]; !ok {
			return fmt.Errorf("check for non-applicable rule")
		}
		key := check.Path + "\x00" + check.Code
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate check")
		}
		seen[key] = struct{}{}
		switch check.Status {
		case "pass", "violation", "inconclusive":
		default:
			return fmt.Errorf("invalid check status")
		}
		if check.Noul == nil || *check.Noul < 0 || *check.Noul > 1 || isInvalidFloat(*check.Noul) {
			return fmt.Errorf("invalid check score")
		}
	}
	if len(result.Errors) == 0 && len(seen) != len(expected) {
		return fmt.Errorf("incomplete check set")
	}
	for _, runErr := range result.Errors {
		if runErr.Path != "" && runErr.Path != filename {
			return fmt.Errorf("unexpected error path")
		}
		if runErr.Code != "" {
			if _, ok := known[runErr.Code]; !ok {
				return fmt.Errorf("unknown error code")
			}
		}
		if runErr.HTTPStatus < 0 || runErr.HTTPStatus > 999 {
			return fmt.Errorf("invalid error HTTP status")
		}
	}
	if _, applies := expected[target]; applies && len(result.Errors) == 0 {
		if _, ok := seen[filename+"\x00"+target]; !ok {
			return fmt.Errorf("target check missing")
		}
	}
	return nil
}

func cliExitCode(result cliResult) int {
	if len(result.Errors) > 0 {
		return 2
	}
	for _, check := range result.Checks {
		if check.Status == "inconclusive" {
			return 2
		}
	}
	for _, check := range result.Checks {
		if check.Status == "violation" {
			return 1
		}
	}
	return 0
}

func evalStatusForScore(rule string, score float64, thresholds map[string]EffectiveThreshold) string {
	threshold, ok := thresholds[rule]
	if !ok {
		return "inconclusive"
	}
	if score < threshold.PassBelow {
		return "pass"
	}
	if score >= threshold.FailAtOrAbove {
		return "violation"
	}
	return "inconclusive"
}

func evaluatedStatus(observation CaseResult) string {
	if observation.EvalStatus != "" {
		return observation.EvalStatus
	}
	return observation.TargetStatus
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func isInvalidFloat(value float64) bool {
	return value != value || value > 1e308 || value < -1e308
}

func processErrorKind(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if err == nil {
		return "empty_output"
	}
	return "process"
}

func prepareOutputDir(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory must be empty")
	}
	return nil
}

func WriteArtifacts(directory string, result RunResults) error {
	if err := writeResults(directory, result); err != nil {
		return err
	}
	return writeSummary(directory, result)
}

func writeResults(directory string, result RunResults) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode results: %w", err)
	}
	data = append(data, '\n')
	temporary := filepath.Join(directory, "results.json.tmp")
	final := filepath.Join(directory, "results.json")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish results: %w", err)
	}
	return nil
}

func resolveJeffBinary(ctx context.Context, options RunOptions) (string, func(), error) {
	if options.JeffBinary != "" {
		path, err := cleanRegularPath(options.JeffBinary)
		if err != nil {
			return "", func() {}, fmt.Errorf("Jeff binary: %w", err)
		}
		return path, func() {}, nil
	}
	root := options.RepoRoot
	if root == "" {
		root = findRepoRoot()
	}
	if root == "" {
		return "", func() {}, fmt.Errorf("repository root is required to build Jeff")
	}
	buildDir, err := os.MkdirTemp("", "jeff-eval-build-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create build directory: %w", err)
	}
	binary := filepath.Join(buildDir, "jeff")
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/jeff")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		_ = os.RemoveAll(buildDir)
		return "", func() {}, fmt.Errorf("build Jeff: %w (%d bytes of build output)", err, len(output))
	}
	return binary, func() { _ = os.RemoveAll(buildDir) }, nil
}

func findRepoRoot() string {
	current, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func hashFile(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func hashSelection(selected []LoadedCase, options RunOptions) (string, error) {
	values := make([]struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}, len(selected))
	for index, item := range selected {
		values[index].ID = item.ID
		values[index].Source = item.SourceSHA256
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	payload := struct {
		Profile     string `json:"profile"`
		Provider    string `json:"provider"`
		Repetitions int    `json:"repetitions"`
		Model       string `json:"model"`
		Cases       any    `json:"cases"`
	}{profileName(options.Profile), options.Provider, options.Config.Repetitions, options.Config.Model, values}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func hashCatalog(catalog rules.Catalog) (string, error) {
	rulesCopy := append([]rules.Rule(nil), catalog.Rules...)
	sort.Slice(rulesCopy, func(i, j int) bool { return rulesCopy[i].Code < rulesCopy[j].Code })
	payload := struct {
		Version int          `json:"version"`
		Model   string       `json:"model"`
		Rules   []rules.Rule `json:"rules"`
	}{catalog.Version, catalog.Model, rulesCopy}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func gitMetadata(root string) (string, bool) {
	if root == "" {
		return "", false
	}
	commitCommand := exec.Command("git", "rev-parse", "HEAD")
	commitCommand.Dir = root
	commit, err := commitCommand.Output()
	if err != nil {
		return "", false
	}
	statusCommand := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	statusCommand.Dir = root
	status, err := statusCommand.Output()
	if err != nil {
		return strings.TrimSpace(string(commit)), false
	}
	return strings.TrimSpace(string(commit)), len(status) > 0
}

type cappedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	if b.limit > 0 && b.Len()+len(data) > b.limit {
		b.exceeded = true
		return 0, fmt.Errorf("output limit exceeded")
	}
	return b.Buffer.Write(data)
}
