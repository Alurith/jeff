package selfupdate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type fakeBackend struct {
	candidate      releaseCandidate
	found          bool
	detectErr      error
	updateErr      error
	detectCalls    int
	updateCalls    int
	updatePath     string
	updatedVersion string
}

func (f *fakeBackend) detectLatest(context.Context) (releaseCandidate, bool, error) {
	f.detectCalls++
	return f.candidate, f.found, f.detectErr
}

func (f *fakeBackend) update(_ context.Context, candidate releaseCandidate, path string) error {
	f.updateCalls++
	f.updatePath = path
	f.updatedVersion = candidate.version
	return f.updateErr
}

func TestUpdateWithRejectsDevelopmentBuild(t *testing.T) {
	backend := &fakeBackend{}
	_, err := updateWith(context.Background(), DevelopmentVersion, backend, func() (string, error) {
		t.Fatal("executable path should not be resolved")
		return "", nil
	})
	if !errors.Is(err, ErrDevelopmentBuild) {
		t.Fatalf("error = %v, want ErrDevelopmentBuild", err)
	}
	if backend.detectCalls != 0 {
		t.Fatalf("detect calls = %d, want 0", backend.detectCalls)
	}
}

func TestUpdateWithRejectsInvalidVersion(t *testing.T) {
	backend := &fakeBackend{}
	_, err := updateWith(context.Background(), "not-a-version", backend, nil)
	if err == nil {
		t.Fatal("error = nil, want invalid version error")
	}
	if backend.detectCalls != 0 {
		t.Fatalf("detect calls = %d, want 0", backend.detectCalls)
	}
}

func TestUpdateWithReturnsNotFound(t *testing.T) {
	_, err := updateWith(context.Background(), "1.0.0", &fakeBackend{}, nil)
	if !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("error = %v, want ErrReleaseNotFound", err)
	}
}

func TestUpdateWithDoesNotUpdateWhenCurrentIsAtLeastLatest(t *testing.T) {
	for _, test := range []struct {
		name    string
		current string
		latest  string
	}{
		{name: "equal", current: "1.0.0", latest: "1.0.0"},
		{name: "newer", current: "2.0.0", latest: "1.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{candidate: releaseCandidate{version: test.latest}, found: true}
			result, err := updateWith(context.Background(), test.current, backend, func() (string, error) {
				t.Fatal("executable path should not be resolved")
				return "", nil
			})
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if result.Updated || backend.updateCalls != 0 {
				t.Fatalf("result=%#v update calls=%d", result, backend.updateCalls)
			}
		})
	}
}

func TestUpdateWithInstallsNewerRelease(t *testing.T) {
	backend := &fakeBackend{candidate: releaseCandidate{version: "v1.1.0"}, found: true}
	path := filepath.Join(t.TempDir(), "jeff")
	result, err := updateWith(context.Background(), "1.0.0", backend, func() (string, error) { return path, nil })
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.Updated || result.CurrentVersion != "1.0.0" || result.LatestVersion != "1.1.0" {
		t.Fatalf("result = %#v", result)
	}
	if backend.updateCalls != 1 || backend.updatePath != path || backend.updatedVersion != "v1.1.0" {
		t.Fatalf("calls/path/version = %d/%q/%q", backend.updateCalls, backend.updatePath, backend.updatedVersion)
	}
}

func TestUpdateWithPropagatesInstallError(t *testing.T) {
	installErr := errors.New("checksum failed")
	backend := &fakeBackend{candidate: releaseCandidate{version: "1.1.0"}, found: true, updateErr: installErr}
	_, err := updateWith(context.Background(), "1.0.0", backend, func() (string, error) { return filepath.Join(t.TempDir(), "jeff"), nil })
	if !errors.Is(err, installErr) {
		t.Fatalf("error = %v, want install error", err)
	}
}

func TestUpdateWithPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &fakeBackend{}
	_, err := updateWith(ctx, "1.0.0", backend, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if backend.detectCalls != 0 {
		t.Fatalf("detect calls = %d, want 0", backend.detectCalls)
	}
}
