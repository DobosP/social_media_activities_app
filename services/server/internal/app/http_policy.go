package app

import (
	"errors"
	"strings"
)

const DefaultPermissionsPolicy = "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()"

// ValidatePermissionsPolicy accepts the reviewed browser policy or tighter
// geolocation denial. Powerful browser permissions remain immutable denials.
func ValidatePermissionsPolicy(raw string) bool {
	return raw == DefaultPermissionsPolicy || raw == strings.Replace(DefaultPermissionsPolicy, "geolocation=(self)", "geolocation=()", 1)
}

func normalizeHTTPPolicy(c *Config) error {
	for _, entry := range []struct {
		name  string
		value *int64
	}{
		{"MAX_REQUEST_BODY_BYTES", &c.MaxRequestBodyBytes},
		{"DATA_UPLOAD_MAX_MEMORY_SIZE", &c.DataUploadMemoryBytes},
	} {
		if *entry.value == 0 {
			*entry.value = 8 << 20
		}
		if *entry.value < 1 || *entry.value > 8<<20 {
			return errors.New(entry.name + " is invalid")
		}
	}
	if c.PermissionsPolicy == "" {
		c.PermissionsPolicy = DefaultPermissionsPolicy
	}
	if !ValidatePermissionsPolicy(c.PermissionsPolicy) {
		return errors.New("PERMISSIONS_POLICY is invalid")
	}
	if c.LogLevel == "" {
		c.LogLevel = "INFO"
	}
	switch c.LogLevel {
	case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
	default:
		return errors.New("LOG_LEVEL is invalid")
	}
	return nil
}

func requestLogEnabled(level string, status int, panicOccurred bool) bool {
	switch level {
	case "WARNING":
		return status >= 400
	case "ERROR":
		return status >= 500
	case "CRITICAL":
		return panicOccurred
	default:
		return true
	}
}
