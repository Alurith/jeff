package files

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverCanonicalizesRequestedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs, err := Discover(root, []string{"src", "src/."})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Path != "src/main.go" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverMatchesCaseInsensitiveRequestedPathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive path behavior is platform-specific")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs, err := Discover(root, []string{"SRC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Path != "src/main.go" {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestDiscoverUsesSourceDirectoriesAndAdditionalIncludes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"src/main.go",
		"lib/helper.go",
		"docs/readme.go",
		"extra/tool.go",
		"blocked/keep.go",
		"vendor/dependency.go",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	inputs, err := Discover(root, []string{"src", "lib"}, DiscoveryOptions{
		Exclude: []string{"vendor", "docs", "blocked"},
		Include: []string{"extra", "blocked/**/*.go", "vendor/**/*.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(inputs))
	for i, input := range inputs {
		got[i] = input.Path
	}
	want := []string{"extra/tool.go", "lib/helper.go", "src/main.go"}
	if len(got) != len(want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths = %#v, want %#v", got, want)
		}
	}
}
