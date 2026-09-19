package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv(CacheDirEnv, "")
	root := t.TempDir()

	settings, err := Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Path != "" || settings.CacheDir != DefaultCacheDir || settings.OutputFormat != "text" {
		t.Fatalf("settings = %#v", settings)
	}
	if !contains(settings.Exclude, ".git") || !contains(settings.Exclude, DefaultCacheDir) {
		t.Fatalf("default excludes = %#v", settings.Exclude)
	}
}

func TestLoadTOMLAndEnvironmentOverride(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, DefaultFileName)
	content := `rule-files = ["rules/*.yml"]
exclude = ["docs", "generated/*.go"]
include = ["scripts/**/*.go"]
src = ["src", "lib"]
cache-dir = ".cache/jeff"
output-format = "json"
jev-version = "jev-2.0.0"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CacheDirEnv, "  tmp/jeff-cache  ")

	settings, err := Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Path != configPath || settings.CacheDir != "tmp/jeff-cache" || settings.OutputFormat != "json" || settings.JevVersion != "jev-2.0.0" {
		t.Fatalf("settings = %#v", settings)
	}
	if len(settings.RuleFiles) != 1 || settings.RuleFiles[0] != filepath.Join(root, "rules", "*.yml") {
		t.Fatalf("rule files = %#v", settings.RuleFiles)
	}
	if !contains(settings.Exclude, "docs") || !contains(settings.Exclude, "tmp/jeff-cache") || !contains(settings.Include, "scripts/**/*.go") || !contains(settings.Src, "src") {
		t.Fatalf("settings paths = %#v", settings)
	}
}

func TestLoadUsesInvocationRootForRuleFiles(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "custom.toml")
	if err := os.WriteFile(path, []byte("rule-files = [\"rules/*.yml\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.RuleFiles) != 1 || settings.RuleFiles[0] != filepath.Join(root, "rules", "*.yml") {
		t.Fatalf("rule files = %#v", settings.RuleFiles)
	}
}

func TestLoadRetainsJSONHintOnConfigError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.toml")
	if err := os.WriteFile(path, []byte("output-format = \"json\"\nunknown = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(root, path)
	if err == nil || settings.OutputFormat != "json" {
		t.Fatalf("settings=%#v error=%v", settings, err)
	}
}

func TestLoadRejectsUnsafeDiscoveryPatterns(t *testing.T) {
	root := t.TempDir()
	for _, content := range []string{
		"exclude = [\"!keep.go\"]\n",
		"include = [\"../outside\"]\n",
		"src = [\"C:\\\\src\"]\n",
	} {
		path := filepath.Join(root, "custom.toml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root, path); err == nil {
			t.Fatalf("accepted unsafe config %q", content)
		}
	}
}

func TestLoadIgnoresWhitespaceOnlyCacheEnvironment(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DefaultFileName)
	if err := os.WriteFile(path, []byte("cache-dir = \"toml-cache\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CacheDirEnv, "   ")
	settings, err := Load(root, "")
	if err != nil || settings.CacheDir != "toml-cache" {
		t.Fatalf("settings=%#v error=%v", settings, err)
	}
}

func TestLoadRejectsUnknownSetting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.toml")
	if err := os.WriteFile(path, []byte("unknown = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root, path)
	if err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsInvalidOutputFormat(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.toml")
	if err := os.WriteFile(path, []byte("output-format = \"yaml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root, path)
	if err == nil || !strings.Contains(err.Error(), "output-format") {
		t.Fatalf("error = %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
