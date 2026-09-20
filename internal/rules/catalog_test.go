package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmbeddedCatalog(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 || catalog.Model != "jev-1.13.0" {
		t.Fatalf("unexpected catalog metadata: %#v", catalog)
	}
	wantCodes := []string{
		"GEN001", "GEN002", "GEN003", "GEN004",
	}
	if len(catalog.Rules) != len(wantCodes) {
		t.Fatalf("got %d rules, want %d", len(catalog.Rules), len(wantCodes))
	}
	for i, want := range wantCodes {
		if catalog.Rules[i].Code != want {
			t.Fatalf("rule %d = %q, want %q", i, catalog.Rules[i].Code, want)
		}
	}
	if !catalog.Rules[0].Applies("main.go") || !catalog.Rules[0].Applies("internal/main.go") {
		t.Fatal("Go selector did not match expected files")
	}
	if catalog.Rules[0].Applies("README.md") {
		t.Fatal("Go selector matched a Markdown file")
	}
	for _, filename := range []string{"id_rsa.pem", "config.yaml", "data.json", "jeff.toml", ".env", ".config/main.go"} {
		if catalog.Rules[0].Applies(filename) {
			t.Fatalf("embedded rule matched non-source file %q", filename)
		}
	}
}

func TestLoadExternalCatalogAndModelOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.yml")
	content := `family: TEAM
rules:
  - code: TEAM001
    name: custom-check
    message: Custom rule
    scope: file
    files:
      include:
        - "**/*"
      allow-non-source: true
    question:
      type: noul
      instructions: The file satisfies the custom condition.
    decision:
      pass_below: 0.20
      fail_at_or_above: 0.80
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := LoadWithOptions(LoadOptions{ExternalFiles: []string{path}, Model: "jev-2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Model != "jev-2.0.0" || len(catalog.Rules) != 5 || catalog.Rules[len(catalog.Rules)-1].Code != "TEAM001" {
		t.Fatalf("catalog = %#v", catalog)
	}
	if !catalog.Rules[len(catalog.Rules)-1].Applies("config.yaml") {
		t.Fatal("external rule did not opt into non-source files")
	}
}

func TestLoadRejectsMissingExternalRuleFile(t *testing.T) {
	_, err := LoadWithOptions(LoadOptions{ExternalFiles: []string{filepath.Join(t.TempDir(), "missing.yml")}})
	if err == nil {
		t.Fatal("missing external rule file was accepted")
	}
}

func TestDecodeYAMLIsStrict(t *testing.T) {
	var value struct {
		Version int `yaml:"version"`
	}
	if err := decodeYAML("unknown.yml", []byte("version: 1\nextra: true\n"), &value); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if err := decodeYAML("duplicate.yml", []byte("version: 1\nversion: 2\n"), &value); err == nil {
		t.Fatal("duplicate field was accepted")
	}
}
