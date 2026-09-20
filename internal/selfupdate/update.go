package selfupdate

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
	goselfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	DevelopmentVersion = "dev"
	repositoryOwner    = "Alurith"
	repositoryName     = "jeff"
)

var (
	ErrDevelopmentBuild = errors.New("self-update is unavailable for development builds")
	ErrReleaseNotFound  = errors.New("no compatible release found")
)

type Result struct {
	CurrentVersion string
	LatestVersion  string
	Updated        bool
}

type releaseCandidate struct {
	version string
	release *goselfupdate.Release
}

type backend interface {
	detectLatest(context.Context) (releaseCandidate, bool, error)
	update(context.Context, releaseCandidate, string) error
}

type githubBackend struct {
	updater    *goselfupdate.Updater
	repository goselfupdate.Repository
}

func Update(ctx context.Context, currentVersion string) (Result, error) {
	parsedCurrent, err := parseCurrentVersion(ctx, currentVersion)
	if err != nil {
		return Result{}, err
	}

	source, err := goselfupdate.NewGitHubSource(goselfupdate.GitHubConfig{})
	if err != nil {
		return Result{}, fmt.Errorf("create GitHub update source: %w", err)
	}
	updater, err := goselfupdate.NewUpdater(goselfupdate.Config{
		Source: source,
		Validator: &goselfupdate.ChecksumValidator{
			UniqueFilename: "checksums.txt",
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("configure self-update client: %w", err)
	}
	return updateParsed(ctx, parsedCurrent, &githubBackend{
		updater:    updater,
		repository: goselfupdate.NewRepositorySlug(repositoryOwner, repositoryName),
	}, goselfupdate.ExecutablePath)
}

func parseCurrentVersion(ctx context.Context, currentVersion string) (*semver.Version, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if currentVersion == DevelopmentVersion {
		return nil, fmt.Errorf("%w: %q", ErrDevelopmentBuild, currentVersion)
	}
	version, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid current version %q: %w", currentVersion, err)
	}
	return version, nil
}

func updateWith(ctx context.Context, currentVersion string, backend backend, executablePath func() (string, error)) (Result, error) {
	parsedCurrent, err := parseCurrentVersion(ctx, currentVersion)
	if err != nil {
		return Result{}, err
	}
	return updateParsed(ctx, parsedCurrent, backend, executablePath)
}

func updateParsed(ctx context.Context, currentVersion *semver.Version, backend backend, executablePath func() (string, error)) (Result, error) {
	candidate, result, err := latest(ctx, currentVersion, backend)
	if err != nil {
		return Result{}, err
	}
	if !semver.MustParse(result.LatestVersion).GreaterThan(currentVersion) {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	path, err := executablePath()
	if err != nil {
		return result, fmt.Errorf("resolve executable path: %w", err)
	}
	if err := backend.update(ctx, candidate, path); err != nil {
		return result, fmt.Errorf("install release %s: %w", result.LatestVersion, err)
	}
	result.Updated = true
	return result, nil
}

func latest(ctx context.Context, currentVersion *semver.Version, backend backend) (releaseCandidate, Result, error) {
	candidate, found, err := backend.detectLatest(ctx)
	if err != nil {
		return releaseCandidate{}, Result{}, fmt.Errorf("detect latest release: %w", err)
	}
	if !found {
		return releaseCandidate{}, Result{}, ErrReleaseNotFound
	}
	latestVersion, err := semver.NewVersion(candidate.version)
	if err != nil {
		return releaseCandidate{}, Result{}, fmt.Errorf("invalid release version %q: %w", candidate.version, err)
	}
	return candidate, Result{CurrentVersion: currentVersion.String(), LatestVersion: latestVersion.String()}, nil
}

func (b *githubBackend) detectLatest(ctx context.Context) (releaseCandidate, bool, error) {
	release, found, err := b.updater.DetectLatest(ctx, b.repository)
	if err != nil || !found {
		return releaseCandidate{}, found, err
	}
	return releaseCandidate{version: release.Version(), release: release}, true, nil
}

func (b *githubBackend) update(ctx context.Context, candidate releaseCandidate, path string) error {
	if candidate.release == nil {
		return errors.New("release candidate is missing its release")
	}
	return b.updater.UpdateTo(ctx, candidate.release, path)
}
