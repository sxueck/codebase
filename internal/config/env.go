package config

import "os"

// Get returns the first non-empty environment variable from the provided keys.
func Get(keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
