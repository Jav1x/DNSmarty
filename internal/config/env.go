package config

import (
	"fmt"
	"os"
	"strings"
)

func FromEnv(key string) (string, error) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", key, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(key), nil
}

func Must(key string) (string, error) {
	v, err := FromEnv(key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
