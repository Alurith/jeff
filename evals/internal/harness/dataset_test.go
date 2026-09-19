package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"jeff/internal/rules"

	"gopkg.in/yaml.v3"
)

func TestDatasetLoadAndMaterialize(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixtures", "sample.go.src")
	if err := os.MkdirAll(filepath.Dir(fixture), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inline := "package inline\n"
	manifest := writeManifest(t, root, "cases.yaml", "dev", false, []Case{
		{ID: "file-case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Source: Source{Filename: "sample.go", File: stringPtr("fixtures/sample.go.src")}},
		{ID: "inline-case", Rule: "GEN002", Language: "go", Label: LabelAmbiguous, Difficulty: DifficultyMedium, Rationale: "provider score falls inside the decision band", Source: Source{Filename: "inline.go", Inline: &inline}},
	})

	dataset, err := LoadDataset(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Hash == "" || len(dataset.Cases) != 2 {
		t.Fatalf("dataset = %#v", dataset)
	}
	if dataset.Cases[0].SourceSHA256 == "" || string(dataset.Cases[0].SourceBytes) != "package sample\n" {
		t.Fatalf("loaded source = %#v", dataset.Cases[0])
	}

	workspace := filepath.Join(root, "workspace")
	materialized, err := MaterializeCase(workspace, dataset.Cases[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(materialized)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package sample\n" {
		t.Fatalf("materialized source = %q", got)
	}
}

func TestDatasetJSONAndHashCanonicalization(t *testing.T) {
	root := t.TempDir()
	yamlPath := filepath.Join(root, "one.yaml")
	jsonPath := filepath.Join(root, "two.json")
	yamlData := "schema_version: 1\ndataset:\n  name: same\n  version: 1.0.0\n  split: dev\n  complete: false\ncases:\n  - id: same-case\n    rule: GEN001\n    language: go\n    label: clean\n    difficulty: easy\n    source:\n      filename: sample.go\n      inline: \"package sample\\n\"\n"
	jsonData := `{"schema_version":1,"dataset":{"name":"same","version":"1.0.0","split":"dev","complete":false},"cases":[{"id":"same-case","rule":"GEN001","language":"go","label":"clean","difficulty":"easy","source":{"filename":"sample.go","inline":"package sample\n"}}]}`
	if err := os.WriteFile(yamlPath, []byte(yamlData), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(jsonData), 0o644); err != nil {
		t.Fatal(err)
	}
	left, err := LoadDataset(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	right, err := LoadDataset(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if left.Hash != right.Hash {
		t.Fatalf("canonical hashes differ: %s != %s", left.Hash, right.Hash)
	}
	if _, err := json.Marshal(left.Manifest); err != nil {
		t.Fatal(err)
	}
}

func TestDatasetRejectsInvalidManifests(t *testing.T) {
	tests := map[string]string{
		"unknown-field": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
extra: true
cases: []
`,
		"multiple-documents": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
cases: []
---
schema_version: 1
`,
		"dual-source": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
cases:
  - id: x
    rule: GEN001
    language: go
    label: clean
    difficulty: easy
    source: {filename: sample.go, file: sample.src, inline: text}
`,
		"traversal": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
cases:
  - id: x
    rule: GEN001
    language: go
    label: clean
    difficulty: easy
    source: {filename: ../sample.go, inline: text}
`,
		"bad-label": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
cases:
  - id: x
    rule: GEN001
    language: go
    label: maybe
    difficulty: easy
    source: {filename: sample.go, inline: text}
`,
		"not-applicable-but-selected": `schema_version: 1
dataset: {name: x, version: 1.0.0, split: dev, complete: false}
cases:
  - id: x
    rule: GEN001
    language: go
    label: not_applicable
    difficulty: easy
    source: {filename: sample.go, inline: text}
`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cases.yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadDataset(path); err == nil {
				t.Fatal("LoadDataset unexpectedly succeeded")
			}
		})
	}
}

func TestCompleteDatasetRequiresAllRulePairs(t *testing.T) {
	inline := "package sample\n"
	path := writeManifest(t, t.TempDir(), "cases.yaml", "test", true, []Case{{
		ID: "only-case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy,
		Pair: "missing-pair", Rationale: "controlled semantic change", Source: Source{Filename: "sample.go", Inline: &inline},
	}})
	if _, err := LoadDataset(path); err == nil || !strings.Contains(err.Error(), "pair") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompleteDatasetLoadsPairsForCatalog(t *testing.T) {
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := make([]Case, 0, len(catalog.Rules)*2)
	for _, rule := range catalog.Rules {
		pair := strings.ToLower(rule.Code) + "-pair"
		filename := "samples/" + strings.ToLower(rule.Code) + ".go"
		clean := fmt.Sprintf("package sample\n// %s clean\n", rule.Code)
		violation := fmt.Sprintf("package sample\n// %s violation\nfunc changed() {}\n", rule.Code)
		cases = append(cases,
			Case{ID: strings.ToLower(rule.Code) + "-clean", Rule: rule.Code, Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Pair: pair, Rationale: "controlled semantic change", Source: Source{Filename: filename, Inline: stringPtr(clean)}},
			Case{ID: strings.ToLower(rule.Code) + "-violation", Rule: rule.Code, Language: "go", Label: LabelViolation, Difficulty: DifficultyEasy, Pair: pair, Rationale: "controlled semantic change", Source: Source{Filename: filename, Inline: stringPtr(violation)}},
		)
	}
	path := writeManifest(t, t.TempDir(), "cases.yaml", "test", true, cases)
	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Cases) != len(catalog.Rules)*2 || dataset.Hash == "" {
		t.Fatalf("dataset has %d cases and hash %q", len(dataset.Cases), dataset.Hash)
	}
}

func TestDatasetSuiteRejectsSourceReuseAcrossRules(t *testing.T) {
	root := t.TempDir()
	content := "package shared\n"
	dev := writeManifest(t, root, "dev.yaml", "dev", false, []Case{{ID: "dev-case", Rule: "GEN001", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Source: Source{Filename: "sample.go", Inline: &content}}})
	test := writeManifest(t, root, "test.yaml", "test", false, []Case{{ID: "test-case", Rule: "GEN002", Language: "go", Label: LabelClean, Difficulty: DifficultyEasy, Source: Source{Filename: "sample.go", Inline: &content}}})
	if _, err := LoadDatasetSuite(dev, test); err == nil || !strings.Contains(err.Error(), "source hash") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigAndThresholdValidation(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	configPath := filepath.Join(filepath.Dir(filename), "..", "..", "config.yaml")
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Repetitions != 1 || config.Concurrency != 1 || config.Pricing.InputUSDPerMillion == nil || *config.Pricing.InputUSDPerMillion != 0.042 {
		t.Fatalf("config defaults = %#v", config)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(unknownPath, append(raw, []byte("\nunknown: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(unknownPath); err == nil {
		t.Fatal("unknown config field unexpectedly accepted")
	}
	invalid := config
	invalid.Repetitions = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("zero repetitions unexpectedly accepted")
	}
	catalog, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	base, err := EffectiveThresholds(catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	pass, fail := 0.1, 0.9
	override, err := EffectiveThresholds(catalog, map[string]Threshold{"GEN007": {PassBelow: &pass, FailAtOrAbove: &fail}})
	if err != nil {
		t.Fatal(err)
	}
	if base["GEN007"] == override["GEN007"] {
		t.Fatal("threshold override did not change effective value")
	}
	if _, err := EffectiveThresholds(catalog, map[string]Threshold{"NOPE": {PassBelow: &pass, FailAtOrAbove: &fail}}); err == nil {
		t.Fatal("unknown threshold override unexpectedly accepted")
	}
}

func writeManifest(t *testing.T, dir, name, split string, complete bool, cases []Case) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(DatasetManifest{SchemaVersion: DatasetSchemaVersion, Dataset: DatasetMetadata{Name: "test-dataset", Version: "1.0.0", Split: split, Complete: complete}, Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func stringPtr(value string) *string { return &value }
