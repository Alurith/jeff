package cache

import (
	"os"
	"path/filepath"
	"testing"

	"jeff/internal/typesafe"
)

func TestStoreRoundTripAndInferenceKeyBoundaries(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "v1"), false)
	question := typesafe.Question{Type: "noul", Instructions: "Is this true?"}
	if _, ok := store.Get("jev-1.13.0", "source", question); ok {
		t.Fatal("unexpected cache hit")
	}
	if err := store.Put("jev-1.13.0", "source", question, 0.75); err != nil {
		t.Fatal(err)
	}
	entry, ok := store.Get("jev-1.13.0", "source", question)
	if !ok || entry.Noul != 0.75 {
		t.Fatalf("entry=%#v hit=%v", entry, ok)
	}
	if _, ok := store.Get("jev-1.13.0", "changed", question); ok {
		t.Fatal("state change reused cache")
	}
	if _, ok := store.Get("jev-1.13.0", "source", typesafe.Question{Type: "noul", Instructions: "changed"}); ok {
		t.Fatal("question change reused cache")
	}
	if _, ok := store.Get("jev-1.13.0", "source", question); !ok {
		t.Fatal("message-independent cache miss")
	}
}

func TestStoreTreatsIncompleteEntriesAsMisses(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "v1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store := New(directory, false)
	question := typesafe.Question{Type: "noul", Instructions: "Is this true?"}
	keyValue, err := key("jev-1.13.0", "source", question)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, keyValue+".json")
	for name, data := range map[string]string{
		"missing schema":  `{"model":"jev-1.13.0","noul":0.5}`,
		"missing noul":    `{"schema_version":1,"model":"jev-1.13.0"}`,
		"null noul":       `{"schema_version":1,"model":"jev-1.13.0","noul":null}`,
		"negative noul":   `{"schema_version":1,"model":"jev-1.13.0","noul":-0.1}`,
		"noul above one":  `{"schema_version":1,"model":"jev-1.13.0","noul":1.1}`,
		"non-finite noul": `{"schema_version":1,"model":"jev-1.13.0","noul":1e999}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filename, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok := store.Get("jev-1.13.0", "source", question); ok {
				t.Fatal("incomplete cache entry was accepted")
			}
		})
	}
}

func TestStoreAcceptsExplicitZeroNoul(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "v1"), false)
	question := typesafe.Question{Type: "noul", Instructions: "Is this true?"}
	if err := store.Put("jev-1.13.0", "source", question, 0); err != nil {
		t.Fatal(err)
	}
	entry, ok := store.Get("jev-1.13.0", "source", question)
	if !ok || entry.Noul != 0 {
		t.Fatalf("entry=%#v hit=%v", entry, ok)
	}
}
