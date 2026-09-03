// Package api exposes the core HTTP API surface and shared input validation
// helpers used by public handlers and module lifecycle entry points.
package api

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxModuleIDLen    = 128
	maxConfigKeyLen   = 64
	maxConfigValLen   = 4096
	maxPathParamLen   = 256
	maxNodeIDLen      = 128
	maxTaskIDLen      = 128
	maxConfigEntries  = 64
	maxQueryParamLen  = 128
)

var (
	moduleIDPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	configKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	pathParamPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]*$`)
	queryParamPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)
)

// ValidateModuleID checks that a module identifier is safe for registry and path use.
func ValidateModuleID(id string) error {
	if id == "" {
		return fmt.Errorf("module ID is required")
	}
	if len(id) > maxModuleIDLen {
		return fmt.Errorf("module ID exceeds maximum length %d", maxModuleIDLen)
	}
	if strings.Contains(id, "..") || strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("module ID contains invalid characters")
	}
	if !moduleIDPattern.MatchString(id) {
		return fmt.Errorf("module ID %q contains invalid characters", id)
	}
	return nil
}

// ValidateTaskID checks task identifiers used in /api/v1/tasks routes.
func ValidateTaskID(id string) error {
	if err := ValidatePathParam("task ID", id); err != nil {
		return err
	}
	if len(id) > maxTaskIDLen {
		return fmt.Errorf("task ID exceeds maximum length %d", maxTaskIDLen)
	}
	return nil
}

// ValidateNodeID checks cluster node identifiers in task reassignment requests.
func ValidateNodeID(id string) error {
	if id == "" {
		return fmt.Errorf("node is required")
	}
	if len(id) > maxNodeIDLen {
		return fmt.Errorf("node exceeds maximum length %d", maxNodeIDLen)
	}
	if strings.Contains(id, "..") || strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("node contains invalid characters")
	}
	if !pathParamPattern.MatchString(id) {
		return fmt.Errorf("node %q contains invalid characters", id)
	}
	return nil
}

// ValidatePathParam checks generic path-segment parameters for traversal and charset issues.
func ValidatePathParam(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxPathParamLen {
		return fmt.Errorf("%s exceeds maximum length %d", name, maxPathParamLen)
	}
	if strings.Contains(value, "..") || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains invalid characters", name)
	}
	if !pathParamPattern.MatchString(value) {
		return fmt.Errorf("%s %q contains invalid characters", name, value)
	}
	return nil
}

// ValidateQueryParam checks optional filter query parameters.
func ValidateQueryParam(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxQueryParamLen {
		return fmt.Errorf("%s exceeds maximum length %d", name, maxQueryParamLen)
	}
	if strings.Contains(value, "..") || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains invalid characters", name)
	}
	if !queryParamPattern.MatchString(value) {
		return fmt.Errorf("%s %q contains invalid characters", name, value)
	}
	return nil
}

// ValidateConfigKey checks module config keys before they are exported as env vars.
func ValidateConfigKey(key string) error {
	if key == "" {
		return fmt.Errorf("config key is required")
	}
	if len(key) > maxConfigKeyLen {
		return fmt.Errorf("config key exceeds maximum length %d", maxConfigKeyLen)
	}
	if strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("config key contains invalid characters")
	}
	if !configKeyPattern.MatchString(key) {
		return fmt.Errorf("config key %q contains invalid characters", key)
	}
	return nil
}

// SanitizeConfigValue strips dangerous control characters from config values.
func SanitizeConfigValue(value string) (string, error) {
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("config value must not contain null bytes")
	}
	if len(value) > maxConfigValLen {
		return "", fmt.Errorf("config value exceeds maximum length %d", maxConfigValLen)
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, value)
	return cleaned, nil
}

// SanitizeModuleConfig validates and returns a sanitized copy of a module config map.
func SanitizeModuleConfig(config map[string]string) (map[string]string, error) {
	if len(config) == 0 {
		return nil, nil
	}
	if len(config) > maxConfigEntries {
		return nil, fmt.Errorf("config exceeds maximum of %d entries", maxConfigEntries)
	}
	out := make(map[string]string, len(config))
	for k, v := range config {
		if err := ValidateConfigKey(k); err != nil {
			return nil, fmt.Errorf("config key: %w", err)
		}
		sv, err := SanitizeConfigValue(v)
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %w", k, err)
		}
		out[k] = sv
	}
	return out, nil
}
