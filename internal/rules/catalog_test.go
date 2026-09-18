package rules

import "testing"

func TestLoadEmbeddedCatalog(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 || catalog.Model != "jev-1.13.0" {
		t.Fatalf("unexpected catalog metadata: %#v", catalog)
	}
	if len(catalog.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(catalog.Rules))
	}
	if !catalog.Rules[0].Applies("main.go") || !catalog.Rules[0].Applies("internal/main.go") {
		t.Fatal("Go selector did not match expected files")
	}
	if catalog.Rules[0].Applies("README.md") {
		t.Fatal("Go selector matched a Markdown file")
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
