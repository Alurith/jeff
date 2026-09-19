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
	wantCodes := []string{
		"GEN001", "GEN002", "GEN003", "GEN004", "GEN005",
		"GEN006", "GEN007", "GEN008", "GEN009", "GEN010",
		"GEN011", "GEN012", "GEN013", "GEN014", "GEN015",
		"GEN016", "GEN017", "GEN018", "GEN019", "GEN020",
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
