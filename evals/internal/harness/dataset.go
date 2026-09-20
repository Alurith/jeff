package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"jeff/internal/rules"

	"gopkg.in/yaml.v3"
)

const (
	DatasetSchemaVersion = 1
	DefaultManifestBytes = 1 << 20
	DefaultSourceBytes   = 64 << 10
	DefaultCaseCount     = 1000
)

type Label string

type Difficulty string

const (
	LabelViolation     Label = "violation"
	LabelClean         Label = "clean"
	LabelAmbiguous     Label = "ambiguous"
	LabelNotApplicable Label = "not_applicable"

	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type DatasetManifest struct {
	SchemaVersion int             `yaml:"schema_version"`
	Dataset       DatasetMetadata `yaml:"dataset"`
	Cases         []Case          `yaml:"cases"`
}

type DatasetMetadata struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	Split    string `yaml:"split"`
	Complete bool   `yaml:"complete"`
}

type Case struct {
	ID               string     `yaml:"id"`
	Rule             string     `yaml:"rule"`
	Language         string     `yaml:"language"`
	Label            Label      `yaml:"label"`
	Difficulty       Difficulty `yaml:"difficulty"`
	Pair             string     `yaml:"pair"`
	Rationale        string     `yaml:"rationale"`
	Tags             []string   `yaml:"tags"`
	AllowActivations []string   `yaml:"allow_activations"`
	Source           Source     `yaml:"source"`
}

type Source struct {
	Filename string  `yaml:"filename"`
	File     *string `yaml:"file"`
	Inline   *string `yaml:"inline"`
}

type LoadedCase struct {
	Case
	SourceBytes  []byte
	SourceSHA256 string
}

type Dataset struct {
	Path     string
	Manifest DatasetManifest
	Cases    []LoadedCase
	Hash     string
}

type DatasetSuite struct {
	Datasets []Dataset
	Hash     string
}

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func LoadDataset(filename string) (Dataset, error) {
	manifestPath, err := cleanRegularPath(filename)
	if err != nil {
		return Dataset{}, fmt.Errorf("dataset: %w", err)
	}
	data, err := readBoundedFile(manifestPath, DefaultManifestBytes)
	if err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: %w", filename, err)
	}
	var manifest DatasetManifest
	if err := decodeStrict(data, filename, &manifest); err != nil {
		return Dataset{}, err
	}
	if err := validateManifestMetadata(manifest); err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: %w", filename, err)
	}

	catalog, err := rules.Load()
	if err != nil {
		return Dataset{}, fmt.Errorf("load catalog: %w", err)
	}
	catalogRules := make(map[string]rules.Rule, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		catalogRules[rule.Code] = rule
	}

	loaded := Dataset{Path: manifestPath, Manifest: manifest, Cases: make([]LoadedCase, 0, len(manifest.Cases))}
	ids := make(map[string]struct{}, len(manifest.Cases))
	sourceKeys := make(map[string]string, len(manifest.Cases))
	for index, item := range manifest.Cases {
		if err := validateCaseMetadata(item, index, ids); err != nil {
			return Dataset{}, fmt.Errorf("dataset %s: %w", filename, err)
		}
		rule, ok := catalogRules[item.Rule]
		if !ok {
			return Dataset{}, fmt.Errorf("dataset %s: case %q: unknown rule %q", filename, item.ID, item.Rule)
		}
		if err := validateAllowedActivations(item, catalogRules); err != nil {
			return Dataset{}, fmt.Errorf("dataset %s: %w", filename, err)
		}
		source, err := loadSource(filepath.Dir(manifestPath), item.Source)
		if err != nil {
			return Dataset{}, fmt.Errorf("dataset %s: case %q: %w", filename, item.ID, err)
		}
		if err := validateApplicability(rule, item); err != nil {
			return Dataset{}, fmt.Errorf("dataset %s: case %q: %w", filename, item.ID, err)
		}
		sourceHash := hashBytes(source)
		key := sourceHash
		if previous, exists := sourceKeys[key]; exists {
			return Dataset{}, fmt.Errorf("dataset %s: cases %q and %q reuse source hash %s", filename, previous, item.ID, sourceHash)
		}
		sourceKeys[key] = item.ID
		loaded.Cases = append(loaded.Cases, LoadedCase{Case: item, SourceBytes: source, SourceSHA256: sourceHash})
	}
	if err := validatePairs(manifest, loaded.Cases); err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: %w", filename, err)
	}
	loaded.Hash, err = hashDataset(manifest, loaded.Cases)
	if err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: hash: %w", filename, err)
	}
	return loaded, nil
}

func LoadDatasetSuite(filenames ...string) (DatasetSuite, error) {
	if len(filenames) == 0 {
		return DatasetSuite{}, fmt.Errorf("dataset suite must contain at least one manifest")
	}
	datasets := make([]Dataset, 0, len(filenames))
	for _, filename := range filenames {
		dataset, err := LoadDataset(filename)
		if err != nil {
			return DatasetSuite{}, err
		}
		datasets = append(datasets, dataset)
	}
	if err := ValidateDatasetSuite(datasets...); err != nil {
		return DatasetSuite{}, err
	}
	canonical := make([]struct {
		Split string `json:"split"`
		Hash  string `json:"hash"`
	}, len(datasets))
	for index, dataset := range datasets {
		canonical[index].Split = dataset.Manifest.Dataset.Split
		canonical[index].Hash = dataset.Hash
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Split < canonical[j].Split })
	data, err := json.Marshal(canonical)
	if err != nil {
		return DatasetSuite{}, fmt.Errorf("hash dataset suite: %w", err)
	}
	return DatasetSuite{Datasets: datasets, Hash: hashBytes(data)}, nil
}

func ValidateDatasetSuite(datasets ...Dataset) error {
	if len(datasets) == 0 {
		return fmt.Errorf("dataset suite must contain at least one manifest")
	}
	seenSplits := make(map[string]string, len(datasets))
	seenIDs := make(map[string]string)
	seenSources := make(map[string]string)
	for _, dataset := range datasets {
		split := dataset.Manifest.Dataset.Split
		if previous, exists := seenSplits[split]; exists {
			return fmt.Errorf("duplicate dataset split %q in %s and %s", split, previous, dataset.Path)
		}
		seenSplits[split] = dataset.Path
		for _, item := range dataset.Cases {
			if previous, exists := seenIDs[item.ID]; exists {
				return fmt.Errorf("case id %q is present in %s and %s", item.ID, previous, dataset.Path)
			}
			seenIDs[item.ID] = dataset.Path
			key := item.SourceSHA256
			if previous, exists := seenSources[key]; exists {
				return fmt.Errorf("source hash %s is reused in %s and %s", item.SourceSHA256, previous, dataset.Path)
			}
			seenSources[key] = dataset.Path
		}
	}
	return nil
}

func MaterializeCase(root string, item LoadedCase) (string, error) {
	if err := validateRelativePath(item.Source.Filename, "source.filename"); err != nil {
		return "", err
	}
	if err := ensureDirectory(root); err != nil {
		return "", fmt.Errorf("materialize root: %w", err)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve materialize root: %w", err)
	}
	destination := filepath.Join(root, filepath.FromSlash(item.Source.Filename))
	if err := ensureNoSymlinkPath(root, destination); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", fmt.Errorf("create materialized directory: %w", err)
	}
	if err := os.WriteFile(destination, item.SourceBytes, 0o644); err != nil {
		return "", fmt.Errorf("write materialized source: %w", err)
	}
	return destination, nil
}

func validateManifestMetadata(manifest DatasetManifest) error {
	if manifest.SchemaVersion != DatasetSchemaVersion {
		return fmt.Errorf("schema_version must be %d", DatasetSchemaVersion)
	}
	if strings.TrimSpace(manifest.Dataset.Name) == "" {
		return fmt.Errorf("dataset.name must not be empty")
	}
	if strings.TrimSpace(manifest.Dataset.Version) == "" {
		return fmt.Errorf("dataset.version must not be empty")
	}
	if manifest.Dataset.Split != "dev" && manifest.Dataset.Split != "test" && manifest.Dataset.Split != "holdout" {
		return fmt.Errorf("dataset.split must be dev, test, or holdout")
	}
	if len(manifest.Cases) == 0 {
		return fmt.Errorf("cases must not be empty")
	}
	if len(manifest.Cases) > DefaultCaseCount {
		return fmt.Errorf("cases exceeds limit %d", DefaultCaseCount)
	}
	return nil
}

func validateCaseMetadata(item Case, index int, ids map[string]struct{}) error {
	if strings.TrimSpace(item.ID) == "" || !safeIDPattern.MatchString(item.ID) {
		return fmt.Errorf("case %d: id must match %s", index, safeIDPattern)
	}
	if _, exists := ids[item.ID]; exists {
		return fmt.Errorf("case %q: duplicate id", item.ID)
	}
	ids[item.ID] = struct{}{}
	if strings.TrimSpace(item.Rule) == "" {
		return fmt.Errorf("case %q: rule must not be empty", item.ID)
	}
	if strings.TrimSpace(item.Language) == "" || strings.ContainsAny(item.Language, "\x00\r\n") {
		return fmt.Errorf("case %q: language must be a non-empty single line", item.ID)
	}
	switch item.Label {
	case LabelViolation, LabelClean, LabelAmbiguous, LabelNotApplicable:
	default:
		return fmt.Errorf("case %q: invalid label %q", item.ID, item.Label)
	}
	switch item.Difficulty {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
	default:
		return fmt.Errorf("case %q: invalid difficulty %q", item.ID, item.Difficulty)
	}
	if item.Pair != "" && !safeIDPattern.MatchString(item.Pair) {
		return fmt.Errorf("case %q: pair must match %s", item.ID, safeIDPattern)
	}
	if item.Pair != "" && item.Label != LabelClean && item.Label != LabelViolation {
		return fmt.Errorf("case %q: pair is only valid for clean or violation", item.ID)
	}
	if item.Pair != "" && strings.TrimSpace(item.Rationale) == "" {
		return fmt.Errorf("case %q: pair rationale must not be empty", item.ID)
	}
	if (item.Label == LabelAmbiguous || item.Label == LabelNotApplicable) && strings.TrimSpace(item.Rationale) == "" {
		return fmt.Errorf("case %q: rationale must not be empty for %s", item.ID, item.Label)
	}
	if err := validateRelativePath(item.Source.Filename, "source.filename"); err != nil {
		return fmt.Errorf("case %q: %w", item.ID, err)
	}
	return nil
}

func validateAllowedActivations(item Case, catalog map[string]rules.Rule) error {
	seen := make(map[string]struct{}, len(item.AllowActivations))
	for _, code := range item.AllowActivations {
		if _, ok := catalog[code]; !ok {
			return fmt.Errorf("case %q: allow_activations references unknown rule %q", item.ID, code)
		}
		if code == item.Rule {
			return fmt.Errorf("case %q: allow_activations cannot contain target rule %q", item.ID, code)
		}
		if _, exists := seen[code]; exists {
			return fmt.Errorf("case %q: allow_activations contains duplicate rule %q", item.ID, code)
		}
		seen[code] = struct{}{}
	}
	return nil
}

func validateApplicability(rule rules.Rule, item Case) error {
	applicable := rule.Applies(item.Source.Filename)
	switch item.Label {
	case LabelNotApplicable:
		if applicable {
			return fmt.Errorf("rule %s applies to filename %q", item.Rule, item.Source.Filename)
		}
	default:
		if !applicable {
			return fmt.Errorf("rule %s does not apply to filename %q", item.Rule, item.Source.Filename)
		}
	}
	return nil
}

func validatePairs(manifest DatasetManifest, cases []LoadedCase) error {
	groups := make(map[string][]LoadedCase)
	for _, item := range cases {
		if item.Pair != "" {
			groups[item.Pair] = append(groups[item.Pair], item)
		}
	}
	for pair, members := range groups {
		if len(members) > 2 {
			return fmt.Errorf("pair %q has %d cases; want at most 2", pair, len(members))
		}
		if len(members) == 2 {
			left, right := members[0], members[1]
			if left.Label == right.Label || left.Rule != right.Rule || left.Language != right.Language || left.Source.Filename != right.Source.Filename {
				return fmt.Errorf("pair %q must have opposite labels and matching rule, language, and filename", pair)
			}
			if left.SourceSHA256 == right.SourceSHA256 {
				return fmt.Errorf("pair %q has identical sources", pair)
			}
		}
	}
	if !manifest.Dataset.Complete {
		return nil
	}
	for _, item := range cases {
		if (item.Label == LabelClean || item.Label == LabelViolation) && item.Pair == "" {
			return fmt.Errorf("complete dataset: binary case %q must have a pair", item.ID)
		}
	}
	byRule := make(map[string]bool)
	for pair, members := range groups {
		if len(members) != 2 || members[0].Label == members[1].Label {
			return fmt.Errorf("complete dataset: pair %q must contain exactly one clean and one violation", pair)
		}
		byRule[members[0].Rule] = true
	}
	catalog, err := rules.Load()
	if err != nil {
		return fmt.Errorf("load catalog for complete validation: %w", err)
	}
	for _, rule := range catalog.Rules {
		if !byRule[rule.Code] {
			return fmt.Errorf("complete dataset: rule %s has no clean/violation pair", rule.Code)
		}
	}
	return nil
}

func loadSource(baseDir string, source Source) ([]byte, error) {
	if (source.File == nil) == (source.Inline == nil) {
		return nil, fmt.Errorf("source must contain exactly one of file or inline")
	}
	var data []byte
	var err error
	if source.File != nil {
		if err := validateRelativePath(*source.File, "source.file"); err != nil {
			return nil, err
		}
		filename := filepath.Join(baseDir, filepath.FromSlash(*source.File))
		if err := ensureNoSymlinkPath(baseDir, filename); err != nil {
			return nil, err
		}
		data, err = readBoundedFile(filename, DefaultSourceBytes)
		if err != nil {
			return nil, fmt.Errorf("read source file: %w", err)
		}
	} else {
		data = []byte(*source.Inline)
		if len(data) > DefaultSourceBytes {
			return nil, fmt.Errorf("inline source exceeds limit %d", DefaultSourceBytes)
		}
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("source is not valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("source contains a NUL byte")
	}
	return append([]byte(nil), data...), nil
}

func hashDataset(manifest DatasetManifest, cases []LoadedCase) (string, error) {
	canonicalCases := make([]canonicalCase, 0, len(cases))
	for _, item := range cases {
		tags := append([]string(nil), item.Tags...)
		sort.Strings(tags)
		allowActivations := append([]string(nil), item.AllowActivations...)
		sort.Strings(allowActivations)
		canonicalCases = append(canonicalCases, canonicalCase{
			ID: item.ID, Rule: item.Rule, Language: item.Language, Label: string(item.Label), Difficulty: string(item.Difficulty), Pair: item.Pair, Rationale: item.Rationale, Tags: tags, AllowActivations: allowActivations, Filename: item.Source.Filename, Source: string(item.SourceBytes),
		})
	}
	sort.Slice(canonicalCases, func(i, j int) bool { return canonicalCases[i].ID < canonicalCases[j].ID })
	canonical := struct {
		SchemaVersion int             `json:"schema_version"`
		Dataset       DatasetMetadata `json:"dataset"`
		Cases         []canonicalCase `json:"cases"`
	}{manifest.SchemaVersion, manifest.Dataset, canonicalCases}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

type canonicalCase struct {
	ID               string   `json:"id"`
	Rule             string   `json:"rule"`
	Language         string   `json:"language"`
	Label            string   `json:"label"`
	Difficulty       string   `json:"difficulty"`
	Pair             string   `json:"pair,omitempty"`
	Rationale        string   `json:"rationale,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	AllowActivations []string `json:"allow_activations,omitempty"`
	Filename         string   `json:"filename"`
	Source           string   `json:"source"`
}

func decodeStrict(data []byte, name string, destination any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%s: decode YAML: %w", name, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s: multiple YAML documents", name)
		}
		return fmt.Errorf("%s: decode trailing YAML: %w", name, err)
	}
	return nil
}

func cleanRegularPath(filename string) (string, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink paths are not supported")
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path must be a regular file")
	}
	return filepath.Clean(absolute), nil
}

func readBoundedFile(filename string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file exceeds limit %d", maxBytes)
	}
	return data, nil
}

func validateRelativePath(value, field string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if strings.ContainsRune(value, 0) || strings.Contains(value, "\\") {
		return fmt.Errorf("%s contains an invalid path character", field)
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(value) {
		return fmt.Errorf("%s must be relative", field)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return fmt.Errorf("%s must not contain . or .. path components", field)
	}
	return nil
}

func ensureDirectory(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("root must be a non-symlink directory")
	}
	return nil
}

func ensureNoSymlinkPath(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes root")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect path: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path is not supported: %s", filepath.ToSlash(current))
		}
	}
	return nil
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
