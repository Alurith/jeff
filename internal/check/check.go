package check

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"jeff/internal/cache"
	"jeff/internal/files"
	"jeff/internal/rules"
	"jeff/internal/typesafe"
)

type Options struct {
	Root       string
	Paths      []string
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	NoCache    bool
}

type Status string

const (
	StatusPass         Status = "pass"
	StatusViolation    Status = "violation"
	StatusInconclusive Status = "inconclusive"
)

type ErrorKind string

const (
	ErrorKindUsage    ErrorKind = "usage"
	ErrorKindConfig   ErrorKind = "config"
	ErrorKindInput    ErrorKind = "input"
	ErrorKindProvider ErrorKind = "provider"
	ErrorKindInternal ErrorKind = "internal"
)

type Diagnostic struct {
	Path    string   `json:"path"`
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Message string   `json:"message"`
	Status  Status   `json:"status"`
	Noul    *float64 `json:"noul,omitempty"`
}

type RunError struct {
	Kind       ErrorKind `json:"kind"`
	Path       string    `json:"path,omitempty"`
	Code       string    `json:"code,omitempty"`
	Message    string    `json:"message"`
	HTTPStatus int       `json:"http_status,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
}

type RunWarning struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type Result struct {
	SchemaVersion int          `json:"schema_version"`
	Checks        []Diagnostic `json:"checks"`
	Errors        []RunError   `json:"errors"`
	Warnings      []RunWarning `json:"warnings"`
}

type runFailure struct {
	Kind ErrorKind
	Code string
	Err  error
}

func (e *runFailure) Error() string { return e.Err.Error() }
func (e *runFailure) Unwrap() error { return e.Err }

func NewErrorResult(kind ErrorKind, path string, err error) Result {
	result := Result{SchemaVersion: 1, Checks: []Diagnostic{}, Errors: []RunError{}, Warnings: []RunWarning{}}
	result.addError(kind, path, "", err)
	return result
}

func Run(ctx context.Context, options Options) Result {
	result := Result{SchemaVersion: 1, Checks: []Diagnostic{}, Errors: []RunError{}, Warnings: []RunWarning{}}
	catalog, err := rules.Load()
	if err != nil {
		result.addError(ErrorKindConfig, "", "", err)
		return result
	}
	root, err := files.ResolveRoot(options.Root)
	if err != nil {
		result.addError(ErrorKindInput, filepath.ToSlash(options.Root), "", err)
		return result
	}
	inputs, err := files.Discover(root, options.Paths)
	if err != nil {
		result.addError(ErrorKindInput, files.ErrorPath(err), "", err)
		return result
	}
	store := cache.New(filepath.Join(root, ".jeff-cache", "v1"), options.NoCache)
	var client *typesafe.Client
	for _, input := range inputs {
		checks, warnings, runErr := checkFile(ctx, input, catalog, options, store, &client)
		result.Checks = append(result.Checks, checks...)
		result.Warnings = append(result.Warnings, warnings...)
		if runErr != nil {
			kind, code := ErrorKindInternal, ""
			var failure *runFailure
			if errors.As(runErr, &failure) {
				kind, code = failure.Kind, failure.Code
			}
			result.addError(kind, input.Path, code, runErr)
		}
	}
	result.sortChecks()
	return result
}

func checkFile(ctx context.Context, input files.Input, catalog rules.Catalog, options Options, store cache.Store, client **typesafe.Client) ([]Diagnostic, []RunWarning, error) {
	applicable := make([]rules.Rule, 0, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		if rule.Applies(input.Path) {
			applicable = append(applicable, rule)
		}
	}
	if len(applicable) == 0 {
		return nil, nil, nil
	}

	data, err := os.ReadFile(input.Absolute)
	if err != nil {
		return nil, nil, &runFailure{Kind: ErrorKindInput, Err: fmt.Errorf("read input: %w", err)}
	}
	if !utf8.Valid(data) {
		return nil, nil, &runFailure{Kind: ErrorKindInput, Err: fmt.Errorf("input is not valid UTF-8")}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, &runFailure{Kind: ErrorKindInput, Err: fmt.Errorf("input contains a NUL byte")}
	}
	state := string(data)
	answers := make(map[string]typesafe.Answer, len(applicable))
	misses := make(map[string]typesafe.Question, len(applicable))
	for _, rule := range applicable {
		var criteria *typesafe.Criteria
		if rule.Question.Criteria != nil {
			criteria = &typesafe.Criteria{
				True:  rule.Question.Criteria.True,
				False: rule.Question.Criteria.False,
			}
		}
		question := typesafe.Question{
			Type:         rule.Question.Type,
			Instructions: rule.Question.Instructions,
			Criteria:     criteria,
		}
		if entry, ok := store.Get(catalog.Model, state, question); ok {
			noul := entry.Noul
			answers[rule.Code] = typesafe.Answer{Type: "noul", Noul: &noul}
		} else {
			misses[rule.Code] = question
		}
	}

	warnings := []RunWarning{}
	if len(misses) > 0 {
		if options.APIKey == "" {
			return nil, warnings, &runFailure{Kind: ErrorKindConfig, Err: fmt.Errorf("TYPESAFE_API_KEY is required")}
		}
		if *client == nil {
			*client, err = typesafe.NewClient(options.BaseURL, options.APIKey, options.HTTPClient)
			if err != nil {
				return nil, warnings, &runFailure{Kind: ErrorKindConfig, Err: err}
			}
		}
		response, err := (*client).Evaluate(ctx, catalog.Model, state, misses)
		if err != nil {
			code := typesafe.RuleCode(err)
			if code == "" && len(misses) == 1 {
				for code = range misses {
				}
			}
			return nil, warnings, &runFailure{Kind: ErrorKindProvider, Code: code, Err: err}
		}
		for code, answer := range response.Answers {
			answers[code] = answer
			if answer.Noul == nil {
				return nil, warnings, &runFailure{Kind: ErrorKindInternal, Code: code, Err: fmt.Errorf("TypeSafe answer %q has no noul", code)}
			}
			if err := store.Put(catalog.Model, state, misses[code], *answer.Noul); err != nil {
				warnings = append(warnings, RunWarning{Path: input.Path, Message: fmt.Sprintf("cache write: %s", err)})
			}
		}
	}

	checks := make([]Diagnostic, 0, len(applicable))
	for _, rule := range applicable {
		answer := answers[rule.Code]
		noul := *answer.Noul
		status := StatusInconclusive
		if noul < *rule.Decision.PassBelow {
			status = StatusPass
		} else if noul >= *rule.Decision.FailAtOrAbove {
			status = StatusViolation
		}
		checks = append(checks, Diagnostic{
			Path:    input.Path,
			Code:    rule.Code,
			Name:    rule.Name,
			Message: rule.Message,
			Status:  status,
			Noul:    &noul,
		})
	}
	return checks, warnings, nil
}

func (r *Result) addError(kind ErrorKind, path, code string, err error) {
	r.Errors = append(r.Errors, RunError{
		Kind:       kind,
		Path:       filepath.ToSlash(path),
		Code:       code,
		Message:    err.Error(),
		HTTPStatus: typesafe.HTTPStatus(err),
		RequestID:  typesafe.RequestID(err),
	})
}

func (r *Result) sortChecks() {
	sort.Slice(r.Checks, func(i, j int) bool {
		if r.Checks[i].Path != r.Checks[j].Path {
			return r.Checks[i].Path < r.Checks[j].Path
		}
		return r.Checks[i].Code < r.Checks[j].Code
	})
}

func (r Result) ExitCode() int {
	if len(r.Errors) > 0 {
		return 2
	}
	for _, check := range r.Checks {
		if check.Status == StatusInconclusive {
			return 2
		}
	}
	for _, check := range r.Checks {
		if check.Status == StatusViolation {
			return 1
		}
	}
	return 0
}

func WriteText(w io.Writer, result Result) error {
	violations, inconclusive := 0, 0
	for _, check := range result.Checks {
		switch check.Status {
		case StatusViolation:
			violations++
		case StatusInconclusive:
			inconclusive++
		}
		if check.Status != StatusPass {
			if _, err := fmt.Fprintf(w, "%s: %s %s: %s\n", check.Path, check.Code, check.Name, check.Message); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "%d violation(s), %d inconclusive, %d error(s)\n", violations, inconclusive, len(result.Errors))
	return err
}

func WriteWarnings(w io.Writer, result Result) error {
	for _, warning := range result.Warnings {
		if warning.Path == "" {
			if _, err := fmt.Fprintf(w, "warning: %s\n", warning.Message); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "warning: %s: %s\n", warning.Path, warning.Message); err != nil {
			return err
		}
	}
	return nil
}

func WriteErrors(w io.Writer, result Result) error {
	for _, runError := range result.Errors {
		if runError.Path == "" {
			if _, err := fmt.Fprintf(w, "error: %s\n", runError.Message); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "error: %s: %s\n", runError.Path, runError.Message); err != nil {
			return err
		}
	}
	return nil
}
