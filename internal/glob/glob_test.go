package glob

import "testing"

func TestMatchSupportsRootBasenamesAndDoubleStar(t *testing.T) {
	for _, test := range []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", true},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/internal/main.go", false},
		{"src/**/*.go", "src/main.go", true},
		{"src/**/*.go", "src/internal/main.go", true},
		{"vendor", "vendor", true},
		{"docs/", "docs", true},
		{"docs/**", "docs", true},
		{"docs/**", "docs/readme.md", true},
	} {
		if got := Match(test.pattern, test.path); got != test.want {
			t.Errorf("Match(%q, %q) = %v, want %v", test.pattern, test.path, got, test.want)
		}
	}
}

func TestValidateRejectsUnsafeOrInvalidPatterns(t *testing.T) {
	for _, pattern := range []string{"", "/tmp", `C:\\tmp`, `\\server\\share`, "../outside", "foo/["} {
		if err := Validate(pattern); err == nil {
			t.Errorf("Validate(%q) accepted invalid pattern", pattern)
		}
	}
	if err := Validate(`src\**\*.go`); err != nil {
		t.Fatalf("Validate Windows-style relative pattern: %v", err)
	}
}
