package credentials

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestLoadAPIKeyPrefersEnvironment(t *testing.T) {
	keyringCalled := false
	value, err := loadAPIKey(
		func(name string) (string, bool) {
			if name != environment {
				t.Fatalf("environment name = %q", name)
			}
			return "environment-key", true
		},
		func(string, string) (string, error) {
			keyringCalled = true
			return "stored-key", nil
		},
	)
	if err != nil || value != "environment-key" || keyringCalled {
		t.Fatalf("value=%q keyringCalled=%v error=%v", value, keyringCalled, err)
	}
}

func TestLoadAPIKeyFailsClosedOnEmptyEnvironment(t *testing.T) {
	keyringCalled := false
	_, err := loadAPIKey(
		func(string) (string, bool) { return "", true },
		func(string, string) (string, error) {
			keyringCalled = true
			return "stored-key", nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "set but empty") || keyringCalled {
		t.Fatalf("keyringCalled=%v error=%v", keyringCalled, err)
	}
}

func TestLoadAPIKeyFallsBackToKeyring(t *testing.T) {
	value, err := loadAPIKey(
		func(string) (string, bool) { return "", false },
		func(gotService, gotAccount string) (string, error) {
			if gotService != service || gotAccount != account {
				t.Fatalf("keyring identity = %q/%q", gotService, gotAccount)
			}
			return "stored-key", nil
		},
	)
	if err != nil || value != "stored-key" {
		t.Fatalf("value=%q error=%v", value, err)
	}
}

func TestLoadAPIKeyDistinguishesMissingAndBackendFailure(t *testing.T) {
	lookupEnv := func(string) (string, bool) { return "", false }
	_, err := loadAPIKey(lookupEnv, func(string, string) (string, error) { return "", keyring.ErrNotFound })
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing error = %v", err)
	}

	backendErr := errors.New("backend unavailable")
	_, err = loadAPIKey(lookupEnv, func(string, string) (string, error) { return "", backendErr })
	if !errors.Is(err, backendErr) || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("backend error = %v", err)
	}

	_, err = loadAPIKey(lookupEnv, func(string, string) (string, error) { return "", nil })
	if err == nil || !strings.Contains(err.Error(), "stored TypeSafe API key is invalid") {
		t.Fatalf("empty stored key error = %v", err)
	}
}

func TestValidateAPIKeyBoundaries(t *testing.T) {
	for name, value := range map[string]string{
		"empty":     "",
		"too large": strings.Repeat("a", MaxAPIKeyBytes+1),
		"NUL":       "a\x00",
		"LF":        "a\n",
		"CR":        "a\r",
	} {
		t.Run(name, func(t *testing.T) {
			byteErr := ValidateAPIKey([]byte(value))
			stringErr := validateAPIKey(value)
			if byteErr == nil || stringErr == nil || byteErr.Error() != stringErr.Error() {
				t.Fatalf("byte error = %v, string error = %v", byteErr, stringErr)
			}
		})
	}
	if err := validateAPIKey("a\n\x00"); err == nil || err.Error() != "API key contains a NUL character" {
		t.Fatalf("mixed control error = %v", err)
	}
	value := strings.Repeat("a", MaxAPIKeyBytes)
	if byteErr, stringErr := ValidateAPIKey([]byte(value)), validateAPIKey(value); byteErr != nil || stringErr != nil {
		t.Fatalf("max-sized API key: byte error = %v, string error = %v", byteErr, stringErr)
	}
}

func TestSaveAndDeleteAPIKeyUseJeffEntry(t *testing.T) {
	keyring.MockInit()
	value := []byte("stored-key")
	if err := SaveAPIKey(value); err != nil {
		t.Fatal(err)
	}
	stored, err := keyring.Get(service, account)
	if err != nil || stored != "stored-key" {
		t.Fatalf("stored=%q error=%v", stored, err)
	}
	if err := DeleteAPIKey(); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get(service, account); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("deleted entry error = %v", err)
	}
	if err := DeleteAPIKey(); err != nil {
		t.Fatalf("idempotent delete error = %v", err)
	}
}

func TestKeyringWriteFailuresRemainErrors(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	keyring.MockInitWithError(backendErr)
	defer keyring.MockInit()
	if err := SaveAPIKey([]byte("synthetic-key")); !errors.Is(err, backendErr) {
		t.Fatalf("save error = %v", err)
	}
	if err := DeleteAPIKey(); !errors.Is(err, backendErr) {
		t.Fatalf("delete error = %v", err)
	}
}

func TestClearOverwritesBuffer(t *testing.T) {
	value := []byte("secret")
	Clear(value)
	if !bytes.Equal(value, make([]byte, len(value))) {
		t.Fatalf("cleared value = %v", value)
	}
}
