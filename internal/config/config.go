package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jeff/internal/glob"

	"github.com/BurntSushi/toml"
)

const (
	DefaultFileName = "jeff.toml"
	CacheDirEnv     = "JEFF_CACHE_DIR"
	DefaultCacheDir = ".jeff-cache"
)

type Settings struct {
	RuleFiles    []string `toml:"rule-files"`
	Exclude      []string `toml:"exclude"`
	Include      []string `toml:"include"`
	Src          []string `toml:"src"`
	CacheDir     string   `toml:"cache-dir"`
	OutputFormat string   `toml:"output-format"`
	JevVersion   string   `toml:"jev-version"`
}

func Load(root, explicitPath string) (Settings, error) {
	settings := Settings{
		CacheDir:     DefaultCacheDir,
		OutputFormat: "text",
	}
	path, found, err := findConfig(root, explicitPath)
	if err != nil {
		return Settings{}, err
	}
	if !found {
		applyCacheDirEnv(&settings)
		appendCacheDirExclude(root, &settings)
		return settings, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return settings, fmt.Errorf("read config %s: %w", path, err)
	}
	if format := outputFormatHint(data); format != "" {
		settings.OutputFormat = format
	}
	var raw Settings
	metadata, err := toml.Decode(string(data), &raw)
	if err != nil {
		return settings, fmt.Errorf("decode config %s: %w", path, err)
	}
	if keys := metadata.Undecoded(); len(keys) > 0 {
		return settings, fmt.Errorf("%s: unknown setting %q", path, keys[0].String())
	}
	if err := validate(raw); err != nil {
		return settings, fmt.Errorf("%s: %w", path, err)
	}

	settings.RuleFiles = resolvePaths(root, normalizePaths(raw.RuleFiles))
	settings.Exclude = append(settings.Exclude, normalizePaths(raw.Exclude)...)
	settings.Include = normalizePaths(raw.Include)
	settings.Src = normalizePaths(raw.Src)
	if cacheDir := strings.TrimSpace(raw.CacheDir); cacheDir != "" {
		settings.CacheDir = glob.Normalize(cacheDir)
	}
	if format := strings.TrimSpace(raw.OutputFormat); format != "" {
		settings.OutputFormat = format
	}
	settings.JevVersion = strings.TrimSpace(raw.JevVersion)
	applyCacheDirEnv(&settings)
	appendCacheDirExclude(root, &settings)
	return settings, nil
}

func findConfig(root, explicitPath string) (string, bool, error) {
	if explicitPath != "" {
		path := explicitPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		if _, err := os.Stat(path); err != nil {
			return "", false, fmt.Errorf("inspect config %s: %w", path, err)
		}
		return path, true, nil
	}
	path := filepath.Join(root, DefaultFileName)
	if _, err := os.Stat(path); err == nil {
		return path, true, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect config %s: %w", path, err)
	}
	return "", false, nil
}

func validate(settings Settings) error {
	for name, values := range map[string][]string{
		"rule-files": settings.RuleFiles,
		"exclude":    settings.Exclude,
		"include":    settings.Include,
		"src":        settings.Src,
	} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s must not contain empty values", name)
			}
			if name == "exclude" || name == "include" {
				normalized := strings.TrimSpace(glob.Normalize(value))
				if strings.HasPrefix(normalized, "!") {
					return fmt.Errorf("%s patterns must not be negated: %q", name, value)
				}
				if err := glob.Validate(value); err != nil {
					return fmt.Errorf("%s pattern %q: %w", name, value, err)
				}
			}
			if name == "src" {
				if err := glob.Validate(value); err != nil {
					return fmt.Errorf("src path %q: %w", value, err)
				}
			}
		}
	}
	if cacheDir := strings.TrimSpace(settings.CacheDir); cacheDir != "" && !filepath.IsAbs(cacheDir) {
		if err := glob.Validate(cacheDir); err != nil {
			return fmt.Errorf("cache-dir: %w", err)
		}
	}
	if format := strings.TrimSpace(settings.OutputFormat); format != "" && format != "text" && format != "json" {
		return fmt.Errorf("output-format must be text or json")
	}
	return nil
}

func outputFormatHint(data []byte) string {
	var values map[string]any
	if _, err := toml.Decode(string(data), &values); err != nil {
		return ""
	}
	format, ok := values["output-format"].(string)
	if !ok {
		return ""
	}
	format = strings.TrimSpace(format)
	if format == "text" || format == "json" {
		return format
	}
	return ""
}

func normalizePaths(paths []string) []string {
	normalized := make([]string, len(paths))
	for i, path := range paths {
		normalized[i] = glob.Normalize(path)
	}
	return normalized
}

func resolvePaths(base string, paths []string) []string {
	resolved := make([]string, len(paths))
	for i, path := range paths {
		if filepath.IsAbs(path) {
			resolved[i] = filepath.Clean(path)
		} else {
			resolved[i] = filepath.Join(base, path)
		}
	}
	return resolved
}

func applyCacheDirEnv(settings *Settings) {
	if value, ok := os.LookupEnv(CacheDirEnv); ok && strings.TrimSpace(value) != "" {
		settings.CacheDir = strings.TrimSpace(value)
	}
}

func appendCacheDirExclude(root string, settings *Settings) {
	cacheDir := settings.CacheDir
	if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(root, cacheDir)
	}
	relative, err := filepath.Rel(root, cacheDir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return
	}
	relative = filepath.ToSlash(relative)
	if relative == DefaultCacheDir {
		return
	}
	for _, pattern := range settings.Exclude {
		if pattern == relative {
			return
		}
	}
	settings.Exclude = append(settings.Exclude, relative)
}
