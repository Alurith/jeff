package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ignore "github.com/Sriram-PR/go-ignore"
)

type Input struct {
	Absolute string
	Path     string
}

type InputError struct {
	Path string
	Err  error
}

func (e *InputError) Error() string {
	if e.Path == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Err)
}

func (e *InputError) Unwrap() error { return e.Err }

func ErrorPath(err error) string {
	var inputErr *InputError
	if errors.As(err, &inputErr) {
		return filepath.ToSlash(inputErr.Path)
	}
	return ""
}

func ResolveRoot(root string) (string, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("find invocation root: %w", err)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve invocation root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect invocation root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invocation root is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve invocation root: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func Discover(root string, paths []string) ([]Input, error) {
	root, err := ResolveRoot(root)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	found := make(map[string]Input)
	directories := make(map[string]struct{})
	for _, input := range paths {
		if input == "" {
			return nil, &InputError{Err: fmt.Errorf("input path must not be empty")}
		}
		absolute, relative, err := resolvePath(root, input)
		if err != nil {
			return nil, &InputError{Path: input, Err: err}
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return nil, &InputError{Path: input, Err: fmt.Errorf("inspect input: %w", err)}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, &InputError{Path: input, Err: fmt.Errorf("symlink inputs are not supported")}
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil || filepath.Clean(resolved) != filepath.Clean(absolute) {
			return nil, &InputError{Path: input, Err: fmt.Errorf("symlink inputs are not supported")}
		}
		switch {
		case info.Mode().IsRegular():
			// An explicitly named file bypasses discovery ignores.
			found[absolute] = Input{Absolute: absolute, Path: relative}
		case info.IsDir():
			directories[relative] = struct{}{}
		default:
			return nil, &InputError{Path: input, Err: fmt.Errorf("input must be a regular file or directory")}
		}
	}

	if len(directories) > 0 {
		if err := discoverDirectories(root, directories, found); err != nil {
			return nil, err
		}
	}

	inputs := make([]Input, 0, len(found))
	for _, input := range found {
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	return inputs, nil
}

func discoverDirectories(root string, directories map[string]struct{}, found map[string]Input) error {
	matcher := ignore.New()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve discovered path: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if walkErr != nil {
			if entry != nil && entry.IsDir() && !directoryNeeded(relative, directories) {
				return fs.SkipDir
			}
			return &InputError{Path: relative, Err: fmt.Errorf("inspect input: %w", walkErr)}
		}
		if entry.IsDir() {
			if relative != "." && entry.Name() == ".git" {
				return fs.SkipDir
			}
			if !directoryNeeded(relative, directories) || relative != "." && matcher.Match(relative, true) {
				return fs.SkipDir
			}
			return addGitignore(matcher, path, relative)
		}
		if !withinRequestedDirectory(relative, directories) || entry.Type()&os.ModeSymlink != 0 || matcher.Match(relative, false) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return &InputError{Path: relative, Err: fmt.Errorf("inspect input: %w", err)}
		}
		if info.Mode().IsRegular() {
			absolute := filepath.Clean(path)
			found[absolute] = Input{Absolute: absolute, Path: relative}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk inputs: %w", err)
	}
	return nil
}

func addGitignore(matcher *ignore.Matcher, directory, relative string) error {
	path := filepath.Join(directory, ".gitignore")
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	ignorePath := filepath.ToSlash(filepath.Join(relative, ".gitignore"))
	if err != nil {
		return &InputError{Path: ignorePath, Err: fmt.Errorf("inspect ignore file: %w", err)}
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return &InputError{Path: ignorePath, Err: fmt.Errorf("read ignore file: %w", err)}
	}
	basePath := relative
	if basePath == "." {
		basePath = ""
	}
	matcher.AddPatternsWithSource(basePath, path, content)
	return nil
}

func directoryNeeded(path string, directories map[string]struct{}) bool {
	for directory := range directories {
		if within(path, directory) || within(directory, path) {
			return true
		}
	}
	return false
}

func withinRequestedDirectory(path string, directories map[string]struct{}) bool {
	for directory := range directories {
		if within(path, directory) {
			return true
		}
	}
	return false
}

func within(path, directory string) bool {
	return directory == "." || path == directory || strings.HasPrefix(path, directory+"/")
}

func resolvePath(root, input string) (string, string, error) {
	absolute := input
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, absolute)
	}
	absolute = filepath.Clean(absolute)
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("input path is outside the invocation root")
	}
	if relative == "." {
		return absolute, ".", nil
	}
	return absolute, filepath.ToSlash(relative), nil
}
