// Package settings stores non-secret application preferences.
package settings

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type Config struct {
	ProjectID string `json:"project_id"`
}

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

func ValidateProjectID(project string) error {
	if !projectIDPattern.MatchString(project) {
		return errors.New("use a project ID of 6–30 lowercase letters, numbers, or hyphens, starting with a letter and ending with a letter or number")
	}
	return nil
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("could not locate the application configuration directory")
	}
	return filepath.Join(dir, "google-chat-max", "config.json"), nil
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, errors.New("could not read the saved project setting")
	}
	defer f.Close()
	var config Config
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	if decoder.Decode(&config) != nil {
		return Config{}, errors.New("the saved project configuration is invalid; save it again")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("the saved project configuration contains unexpected data")
	}
	if config.ProjectID != "" {
		if err := ValidateProjectID(config.ProjectID); err != nil {
			return config, err
		}
	}
	return config, nil
}

func Save(config Config) error {
	if err := ValidateProjectID(config.ProjectID); err != nil {
		return err
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("could not create the application configuration directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return errors.New("could not write the project configuration")
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return errors.New("could not protect the project configuration")
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(config)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("could not save the project configuration")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return errors.New("could not replace the saved project configuration")
	}
	return nil
}
