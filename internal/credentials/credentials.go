package credentials

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

const (
	MaxAPIKeyBytes = 2048
	environment    = "TYPESAFE_API_KEY"
	service        = "jeff"
	account        = "typesafe-api-key"
)

var ErrNotConfigured = errors.New("TypeSafe API key is not configured")

func LoadAPIKey() (string, error) {
	return loadAPIKey(os.LookupEnv, keyring.Get)
}

func loadAPIKey(lookupEnv func(string) (string, bool), get func(string, string) (string, error)) (string, error) {
	if value, exists := lookupEnv(environment); exists {
		if value == "" {
			return "", fmt.Errorf("%s is set but empty", environment)
		}
		if err := validateAPIKey(value); err != nil {
			return "", fmt.Errorf("%s is invalid: %w", environment, err)
		}
		return value, nil
	}

	value, err := get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("%w; set %s or run %q", ErrNotConfigured, environment, "jeff auth login")
	}
	if err != nil {
		return "", fmt.Errorf("read TypeSafe API key from system keyring: %w", err)
	}
	if err := validateAPIKey(value); err != nil {
		return "", fmt.Errorf("stored TypeSafe API key is invalid: %w", err)
	}
	return value, nil
}

func SaveAPIKey(value []byte) error {
	if err := ValidateAPIKey(value); err != nil {
		return err
	}
	if err := keyring.Set(service, account, string(value)); err != nil {
		return fmt.Errorf("store TypeSafe API key in system keyring: %w", err)
	}
	return nil
}

func DeleteAPIKey() error {
	if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete TypeSafe API key from system keyring: %w", err)
	}
	return nil
}

func ValidateAPIKey(value []byte) error { return validateAPIKey(value) }

func validateAPIKey[T ~string | ~[]byte](value T) error {
	if len(value) == 0 {
		return fmt.Errorf("API key cannot be empty")
	}
	if len(value) > MaxAPIKeyBytes {
		return fmt.Errorf("API key exceeds %d bytes", MaxAPIKeyBytes)
	}
	hasNewline := false
	for i := range len(value) {
		switch value[i] {
		case 0:
			return fmt.Errorf("API key contains a NUL character")
		case '\n', '\r':
			hasNewline = true
		}
	}
	if hasNewline {
		return fmt.Errorf("API key cannot contain newline characters")
	}
	return nil
}

func Clear(value []byte) { clear(value) }
