package rules

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"math"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yml catalog/*.yml
var embeddedCatalog embed.FS

type Catalog struct {
	Version int
	Model   string
	Rules   []Rule
}

type catalogMetadata struct {
	Version int    `yaml:"version"`
	Model   string `yaml:"model"`
}

type fragment struct {
	Family string `yaml:"family"`
	Rules  []Rule `yaml:"rules"`
}

type Rule struct {
	Code     string    `yaml:"code"`
	Name     string    `yaml:"name"`
	Message  string    `yaml:"message"`
	Scope    string    `yaml:"scope"`
	Files    Selectors `yaml:"files"`
	Question Question  `yaml:"question"`
	Decision Decision  `yaml:"decision"`
}

type Selectors struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

type Question struct {
	Type         string    `yaml:"type"`
	Instructions string    `yaml:"instructions"`
	Criteria     *Criteria `yaml:"criteria"`
}

type Criteria struct {
	True  string `yaml:"true"`
	False string `yaml:"false"`
}

type Decision struct {
	PassBelow     *float64 `yaml:"pass_below"`
	FailAtOrAbove *float64 `yaml:"fail_at_or_above"`
}

var (
	familyPattern = regexp.MustCompile(`^[A-Z]{2,8}$`)
	codePattern   = regexp.MustCompile(`^[A-Z]{2,8}[0-9]{3}$`)
	namePattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	modelPattern  = regexp.MustCompile(`^jev-[0-9]+\.[0-9]+\.[0-9]+$`)
)

func Load() (Catalog, error) {
	var metadata catalogMetadata
	if err := decodeYAML("catalog.yml", mustReadEmbedded("catalog.yml"), &metadata); err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{Version: metadata.Version, Model: metadata.Model}
	if catalog.Version != 1 {
		return Catalog{}, fmt.Errorf("catalog.yml: version must be 1")
	}
	if !modelPattern.MatchString(catalog.Model) {
		return Catalog{}, fmt.Errorf("catalog.yml: model must be a concrete Jev version")
	}

	files, err := fs.Glob(embeddedCatalog, "catalog/*.yml")
	if err != nil {
		return Catalog{}, fmt.Errorf("find catalog fragments: %w", err)
	}
	if len(files) == 0 {
		return Catalog{}, fmt.Errorf("catalog: no family fragments")
	}
	sort.Strings(files)

	families := make(map[string]struct{}, len(files))
	codes := make(map[string]struct{})
	names := make(map[string]struct{})
	for _, filename := range files {
		var part fragment
		if err := decodeYAML(filename, mustReadEmbedded(filename), &part); err != nil {
			return Catalog{}, err
		}
		if err := validateFragment(part); err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", filename, err)
		}
		if _, exists := families[part.Family]; exists {
			return Catalog{}, fmt.Errorf("%s: duplicate family %q", filename, part.Family)
		}
		families[part.Family] = struct{}{}
		for _, rule := range part.Rules {
			if _, exists := codes[rule.Code]; exists {
				return Catalog{}, fmt.Errorf("%s: duplicate rule code %q", filename, rule.Code)
			}
			if _, exists := names[rule.Name]; exists {
				return Catalog{}, fmt.Errorf("%s: duplicate rule name %q", filename, rule.Name)
			}
			codes[rule.Code] = struct{}{}
			names[rule.Name] = struct{}{}
			catalog.Rules = append(catalog.Rules, rule)
		}
	}
	return catalog, nil
}

func mustReadEmbedded(name string) []byte {
	data, err := fs.ReadFile(embeddedCatalog, name)
	if err != nil {
		panic(err)
	}
	return data
}

func decodeYAML(name string, data []byte, dst any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%s: decode YAML: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s: multiple YAML documents", name)
		}
		return fmt.Errorf("%s: decode trailing YAML: %w", name, err)
	}
	return nil
}

func validateFragment(part fragment) error {
	if !familyPattern.MatchString(part.Family) {
		return fmt.Errorf("family must be 2-8 uppercase letters")
	}
	if len(part.Rules) == 0 {
		return fmt.Errorf("rules must not be empty")
	}
	for _, rule := range part.Rules {
		if !codePattern.MatchString(rule.Code) || len(rule.Code) != len(part.Family)+3 || !strings.HasPrefix(rule.Code, part.Family) {
			return fmt.Errorf("rule %q: code must be %s followed by three digits", rule.Name, part.Family)
		}
		if !namePattern.MatchString(rule.Name) {
			return fmt.Errorf("rule %q: name must be kebab-case", rule.Code)
		}
		if strings.TrimSpace(rule.Message) == "" {
			return fmt.Errorf("rule %q: message must not be empty", rule.Code)
		}
		if rule.Scope != "file" {
			return fmt.Errorf("rule %q: scope must be file", rule.Code)
		}
		if len(rule.Files.Include) == 0 {
			return fmt.Errorf("rule %q: files.include must not be empty", rule.Code)
		}
		for _, pattern := range append(append([]string{}, rule.Files.Include...), rule.Files.Exclude...) {
			if err := validatePattern(pattern); err != nil {
				return fmt.Errorf("rule %q: %w", rule.Code, err)
			}
		}
		if rule.Question.Type != "noul" {
			return fmt.Errorf("rule %q: question.type must be noul", rule.Code)
		}
		if strings.TrimSpace(rule.Question.Instructions) == "" {
			return fmt.Errorf("rule %q: question.instructions must not be empty", rule.Code)
		}
		if rule.Decision.PassBelow == nil || rule.Decision.FailAtOrAbove == nil {
			return fmt.Errorf("rule %q: both decision bounds are required", rule.Code)
		}
		passBelow, failAtOrAbove := *rule.Decision.PassBelow, *rule.Decision.FailAtOrAbove
		if math.IsNaN(passBelow) || math.IsInf(passBelow, 0) || math.IsNaN(failAtOrAbove) || math.IsInf(failAtOrAbove, 0) || passBelow < 0 || failAtOrAbove > 1 || passBelow > failAtOrAbove {
			return fmt.Errorf("rule %q: decision bounds must satisfy 0 <= pass_below <= fail_at_or_above <= 1", rule.Code)
		}
	}
	return nil
}

func validatePattern(pattern string) error {
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	if pattern == "" || strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("file selector must be a non-empty relative glob")
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := pathpkg.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid file selector %q: %w", pattern, err)
		}
	}
	return nil
}

func (r Rule) Applies(filename string) bool {
	filename = strings.ReplaceAll(filename, "\\", "/")
	filename = strings.TrimPrefix(filename, "./")
	included := false
	for _, pattern := range r.Files.Include {
		if globMatch(pattern, filename) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, pattern := range r.Files.Exclude {
		if globMatch(pattern, filename) {
			return false
		}
	}
	return true
}

func globMatch(pattern, filename string) bool {
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	filename = strings.TrimPrefix(strings.ReplaceAll(filename, "\\", "/"), "./")
	patterns := strings.Split(pattern, "/")
	parts := strings.Split(filename, "/")
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
