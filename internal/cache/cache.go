package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"jeff/internal/typesafe"
)

const protocolVersion = 1

type Store struct {
	directory string
	disabled  bool
}

type storedEntry struct {
	SchemaVersion int      `json:"schema_version"`
	Model         string   `json:"model"`
	Noul          *float64 `json:"noul"`
}

type cacheQuestion struct {
	Type         string             `json:"type"`
	Instructions string             `json:"instructions"`
	Criteria     *typesafe.Criteria `json:"criteria,omitempty"`
}

type cacheInput struct {
	Protocol int           `json:"protocol"`
	Model    string        `json:"model"`
	State    string        `json:"state"`
	Question cacheQuestion `json:"question"`
}

func New(directory string, disabled bool) Store {
	return Store{directory: directory, disabled: disabled}
}

func (s Store) Get(model, state string, question typesafe.Question) (float64, bool) {
	if s.disabled {
		return 0, false
	}
	key, err := key(model, state, question)
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Join(s.directory, key+".json"))
	if err != nil {
		return 0, false
	}
	var stored storedEntry
	if err := json.Unmarshal(data, &stored); err != nil || stored.SchemaVersion != protocolVersion || stored.Model != model || stored.Noul == nil || *stored.Noul < 0 || *stored.Noul > 1 || math.IsNaN(*stored.Noul) || math.IsInf(*stored.Noul, 0) {
		return 0, false
	}
	return *stored.Noul, true
}

func (s Store) Put(model, state string, question typesafe.Question, noul float64) error {
	if s.disabled {
		return nil
	}
	if noul < 0 || noul > 1 || math.IsNaN(noul) || math.IsInf(noul, 0) {
		return fmt.Errorf("invalid noul")
	}
	key, err := key(model, state, question)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return err
	}
	if err := ensureGitignore(s.directory); err != nil {
		return err
	}
	data, err := json.Marshal(storedEntry{SchemaVersion: protocolVersion, Model: model, Noul: &noul})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.directory, ".tmp-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(s.directory, key+".json"))
}

func ensureGitignore(directory string) error {
	filename := filepath.Join(directory, ".gitignore")
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString("*\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(filename)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(filename)
		return err
	}
	return nil
}

func key(model, state string, question typesafe.Question) (string, error) {
	input := cacheInput{
		Protocol: protocolVersion,
		Model:    model,
		State:    state,
		Question: cacheQuestion{Type: question.Type, Instructions: question.Instructions, Criteria: question.Criteria},
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
