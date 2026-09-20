package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSourceFileUsesConservativeAllowlist(t *testing.T) {
	for _, filename := range []string{
		"main.go",
		"scripts/check.py",
		"src/app.ts",
		"Dockerfile",
		"Makefile",
	} {
		if !IsSourceFile(filename) {
			t.Errorf("IsSourceFile(%q) = false, want true", filename)
		}
	}
	for _, filename := range []string{
		"id_rsa.pem",
		"server.key",
		"server.crt",
		"config.yaml",
		"config.yml",
		"data.json",
		"jeff.toml",
		"secrets.tfvars",
		".env",
		".config/main.go",
		"src/.generated.go",
		"vendor/dependency.go",
		"node_modules/package/index.js",
	} {
		if IsSourceFile(filename) {
			t.Errorf("IsSourceFile(%q) = true, want false", filename)
		}
	}
}

func TestDiscoverHonorsGitignoreAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"keep.go", "nested/use.go", "ignored/cache.go", "generated/deep/cache.go", ".git/config.go", ".jeff-cache/v1/entry.go", "vendor/dependency.go", "node_modules/package/index.js"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n*.txt\ngenerated/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inputs, err := Discover(root, []string{".", "keep.go", "nested"}, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || inputs[0].Path != ".gitignore" || inputs[1].Path != "keep.go" || inputs[2].Path != "nested/use.go" {
		t.Fatalf("inputs = %#v", inputs)
	}

	inputs, err = Discover(root, []string{"ignored/cache.go"}, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Path != "ignored/cache.go" {
		t.Fatalf("explicit ignored file = %#v", inputs)
	}

	inputs, err = Discover(root, []string{"nested"}, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Path != "nested/use.go" {
		t.Fatalf("selected directory = %#v", inputs)
	}
}

func TestDiscoverUsesRootRelativeSlashPatterns(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"foo/bar.go", "nested/foo/bar.go"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("foo/bar.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inputs, err := Discover(root, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].Path != ".gitignore" || inputs[1].Path != "nested/foo/bar.go" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverHonorsNestedGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".gitignore":     "*.go\n",
		"sub/.gitignore": "!keep.go\nsecret.txt\n",
		"sub/keep.go":    "package sub\n",
		"sub/secret.txt": "secret\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	inputs, err := Discover(root, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || inputs[0].Path != ".gitignore" || inputs[1].Path != "sub/.gitignore" || inputs[2].Path != "sub/keep.go" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverDoesNotLoadRootSymlinkedGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rules"), []byte("secret.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("rules", filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	inputs, err := Discover(root, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].Path != "rules" || inputs[1].Path != "secret.go" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverDoesNotLoadSymlinkedGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".gitignore":    "*.go\n",
		"override":      "!secret.go\n",
		"sub/secret.go": "package sub\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../override", filepath.Join(root, "sub", ".gitignore")); err != nil {
		t.Fatal(err)
	}

	inputs, err := Discover(root, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].Path != ".gitignore" || inputs[1].Path != "override" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverRejectsSymlinkAndOutsidePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	if err := os.WriteFile(target, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Discover(root, []string{"link.go"}, DiscoveryOptions{}); err == nil {
		t.Fatal("symlink was accepted")
	}
	if _, err := Discover(root, []string{"../outside.go"}, DiscoveryOptions{}); err == nil {
		t.Fatal("outside path was accepted")
	}
}
