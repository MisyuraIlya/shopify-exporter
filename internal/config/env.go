package config

import (
	"fmt"
	"os"
	"strconv"
)

func requriedString(key string) (string, error) {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return "", fmt.Errorf("missing requried env var: %s", key)
	}
	return variable, nil
}

func stringWithDefault(key, def string) string {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return def
	}
	return variable
}

func intWithDefault(key string, def int) (int, error) {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return def, nil
	}
	number, err := strconv.Atoi(key)
	if err != nil {
		return 0, fmt.Errorf("Invalid int for %s: %w", key, err)
	}
	return number, nil
}
