package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	goselfupdate "github.com/creativeprojects/go-selfupdate"
)

func TestPublishedArchiveContract(t *testing.T) {
	const (
		assetName = "jeff_1.1.0_linux_amd64.tar.gz"
		binary    = "new jeff binary"
	)
	archive := tarGzip(t, "jeff", []byte(binary))
	hash := sha256.Sum256(archive)
	updater, release := testUpdater(t, assetName, archive, fmt.Sprintf("%x  %s\n", hash, assetName))

	target := filepath.Join(t.TempDir(), "jeff")
	if err := os.WriteFile(target, []byte("old jeff binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := updater.UpdateTo(context.Background(), release, target); err != nil {
		t.Fatalf("UpdateTo() error = %v", err)
	}
	updated, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != binary {
		t.Fatalf("updated binary = %q, want %q", updated, binary)
	}
}

func TestPublishedArchiveRejectsBadChecksum(t *testing.T) {
	const (
		assetName = "jeff_1.1.0_linux_amd64.tar.gz"
		oldBinary = "old jeff binary"
	)
	archive := tarGzip(t, "jeff", []byte("new jeff binary"))
	updater, release := testUpdater(t, assetName, archive, fmt.Sprintf("%064x  %s\n", 0, assetName))

	target := filepath.Join(t.TempDir(), "jeff")
	if err := os.WriteFile(target, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := updater.UpdateTo(context.Background(), release, target); err == nil {
		t.Fatal("UpdateTo() error = nil, want checksum rejection")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != oldBinary {
		t.Fatalf("target changed after checksum rejection: %q", contents)
	}
}

func testUpdater(t *testing.T, assetName string, archive []byte, checksum string) (*goselfupdate.Updater, *goselfupdate.Release) {
	t.Helper()
	manifest := fmt.Sprintf(`releases:
  - id: 1
    tag_name: v1.1.0
    assets:
      - id: 1
        name: %s
        size: %d
        url: v1.1.0/%s
      - id: 2
        name: checksums.txt
        size: %d
        url: v1.1.0/checksums.txt
`, assetName, len(archive), assetName, len(checksum))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/Alurith/jeff/manifest.yaml":
			_, _ = w.Write([]byte(manifest))
		case "/Alurith/jeff/v1.1.0/" + assetName:
			_, _ = w.Write(archive)
		case "/Alurith/jeff/v1.1.0/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	source, err := goselfupdate.NewHttpSource(goselfupdate.HttpConfig{BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	updater, err := goselfupdate.NewUpdater(goselfupdate.Config{
		Source: source,
		OS:     "linux",
		Arch:   "amd64",
		Validator: &goselfupdate.ChecksumValidator{
			UniqueFilename: "checksums.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	release, found, err := updater.DetectLatest(context.Background(), goselfupdate.NewRepositorySlug("Alurith", "jeff"))
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("DetectLatest() found = false, want true")
	}
	return updater, release
}

func tarGzip(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
