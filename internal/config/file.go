package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func LoadFromUserConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		// Best-effort: if we can't resolve home, just skip file loading.
		return nil
	}

	configPath := filepath.Join(home, ".codebase", "config.json")
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	var cfg map[string]string
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return err
	}

	for key, value := range cfg {
		if value == "" {
			continue
		}
		// Values from ~/.codebase/config.json take precedence over existing env vars.
		_ = os.Setenv(key, value)
	}

	return nil
}
