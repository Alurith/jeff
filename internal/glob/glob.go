package glob

import (
	"fmt"
	pathpkg "path"
	"strings"
)

func Normalize(pattern string) string {
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	for strings.HasPrefix(pattern, "./") {
		pattern = strings.TrimPrefix(pattern, "./")
	}
	for strings.HasSuffix(pattern, "/") && pattern != "/" {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	return pattern
}

func Validate(pattern string) error {
	pattern = Normalize(pattern)
	if pattern == "" || isAbsolute(pattern) {
		return fmt.Errorf("pattern must be a non-empty relative glob")
	}
	clean := pathpkg.Clean(pattern)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("pattern must not escape its root")
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if segment == "" {
			return fmt.Errorf("pattern contains an empty path segment")
		}
		if _, err := pathpkg.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
	}
	return nil
}

func Match(pattern, filename string) bool {
	pattern = Normalize(pattern)
	filename = Normalize(filename)
	if pattern == "" || filename == "" {
		return false
	}
	patterns := strings.Split(pattern, "/")
	parts := strings.Split(filename, "/")
	if len(patterns) == 1 {
		patterns = append([]string{"**"}, patterns...)
	}
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var match func(int, int) bool
	match = func(patternIndex, partIndex int) bool {
		key := [2]int{patternIndex, partIndex}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var result bool
		switch {
		case patternIndex == len(patterns):
			result = partIndex == len(parts)
		case patterns[patternIndex] == "**":
			result = match(patternIndex+1, partIndex) || (partIndex < len(parts) && match(patternIndex, partIndex+1))
		case partIndex < len(parts):
			ok, err := pathpkg.Match(patterns[patternIndex], parts[partIndex])
			result = err == nil && ok && match(patternIndex+1, partIndex+1)
		}
		memo[key] = result
		return result
	}
	return match(0, 0)
}

func isAbsolute(pattern string) bool {
	if strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, "\\") {
		return true
	}
	return len(pattern) >= 2 && pattern[1] == ':' && ((pattern[0] >= 'a' && pattern[0] <= 'z') || (pattern[0] >= 'A' && pattern[0] <= 'Z'))
}
