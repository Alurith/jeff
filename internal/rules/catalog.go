package rules

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"jeff/internal/glob"

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

type LoadOptions struct {
	ExternalFiles []string
	Model         string
}

func Load() (Catalog, error) {
	return LoadWithOptions(LoadOptions{})
}

func LoadWithOptions(options LoadOptions) (Catalog, error) {
	var metadata catalogMetadata
	if err := decodeYAML("catalog.yml", mustReadEmbedded("catalog.yml"), &metadata); err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{Version: metadata.Version, Model: metadata.Model}
	if catalog.Version != 1 {
		return Catalog{}, fmt.Errorf("catalog.yml: version must be 1")
	}
	if err := validateModel(catalog.Model); err != nil {
		return Catalog{}, fmt.Errorf("catalog.yml: %w", err)
	}
	if options.Model != "" {
		if err := validateModel(options.Model); err != nil {
			return Catalog{}, fmt.Errorf("jev-version: %w", err)
		}
		catalog.Model = options.Model
	}

	sources, err := embeddedSources()
	if err != nil {
		return Catalog{}, err
	}
	external, err := externalSources(options.ExternalFiles)
	if err != nil {
		return Catalog{}, err
	}
	sources = append(sources, external...)

	families := make(map[string]struct{}, len(sources))
	codes := make(map[string]struct{})
	names := make(map[string]struct{})
	for _, source := range sources {
		var part fragment
		if err := decodeYAML(source.name, source.data, &part); err != nil {
			return Catalog{}, err
		}
		if err := validateFragment(part); err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", source.name, err)
		}
		if _, exists := families[part.Family]; exists {
			return Catalog{}, fmt.Errorf("%s: duplicate family %q", source.name, part.Family)
		}
		families[part.Family] = struct{}{}
		for _, rule := range part.Rules {
			if _, exists := codes[rule.Code]; exists {
				return Catalog{}, fmt.Errorf("%s: duplicate rule code %q", source.name, rule.Code)
			}
			if _, exists := names[rule.Name]; exists {
				return Catalog{}, fmt.Errorf("%s: duplicate rule name %q", source.name, rule.Name)
			}
			codes[rule.Code] = struct{}{}
			names[rule.Name] = struct{}{}
			catalog.Rules = append(catalog.Rules, rule)
		}
	}
	return catalog, nil
}

type catalogSource struct {
	name string
	data []byte
}

func embeddedSources() ([]catalogSource, error) {
	files, err := fs.Glob(embeddedCatalog, "catalog/*.yml")
	if err != nil {
		return nil, fmt.Errorf("find catalog fragments: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("catalog: no family fragments")
	}
	sort.Strings(files)
	sources := make([]catalogSource, 0, len(files))
	for _, filename := range files {
		sources = append(sources, catalogSource{name: filename, data: mustReadEmbedded(filename)})
	}
	return sources, nil
}

func externalSources(patterns []string) ([]catalogSource, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var sources []catalogSource
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("find external rule files %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("external rule pattern %q matched no files", pattern)
		}
		sort.Strings(matches)
		for _, filename := range matches {
			filename = filepath.Clean(filename)
			if _, exists := seen[filename]; exists {
				continue
			}
			data, err := os.ReadFile(filename)
			if err != nil {
				return nil, fmt.Errorf("read external rule file %s: %w", filename, err)
			}
			seen[filename] = struct{}{}
			sources = append(sources, catalogSource{name: filename, data: data})
		}
	}
	return sources, nil
}

func validateModel(model string) error {
	if !modelPattern.MatchString(model) {
		return fmt.Errorf("model must be a concrete Jev version")
	}
	return nil
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
	if err := glob.Validate(pattern); err != nil {
		return fmt.Errorf("file selector: %w", err)
	}
	return nil
}

func (r Rule) Applies(filename string) bool {
	included := false
	for _, pattern := range r.Files.Include {
		if glob.Match(pattern, filename) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, pattern := range r.Files.Exclude {
		if glob.Match(pattern, filename) {
			return false
		}
	}
	return true
}
