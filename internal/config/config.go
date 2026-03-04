package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apsdsm/meimei/internal/types"
	"gopkg.in/yaml.v3"
)

const configFileName = ".meimei.yaml"

// ExtraColumn defines a project-specific column to show in the builds table.
type ExtraColumn struct {
	Field  string `yaml:"field"`
	Header string `yaml:"header"`
}

// ProjectConfig is the top-level config loaded from .meimei.yaml.
type ProjectConfig struct {
	Project      string                           `yaml:"project"`
	Accounts     map[string]types.AccountConfig   `yaml:"accounts"`
	ExtraColumns []ExtraColumn                    `yaml:"extra_columns"`
	IndexName    string                           `yaml:"index_name"`
}

// FindConfigFile walks up the directory tree from the current working directory
// looking for .meimei.yaml. Returns the full path if found.
func FindConfigFile() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found (searched from working directory to filesystem root)", configFileName)
		}
		dir = parent
	}
}

// Load reads and parses a .meimei.yaml file. If path is empty, it searches
// for the config file by walking up the directory tree.
func Load(path string) (*ProjectConfig, error) {
	if path == "" {
		found, err := FindConfigFile()
		if err != nil {
			return nil, err
		}
		path = found
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Default index name
	if cfg.IndexName == "" {
		cfg.IndexName = "app_name-index"
	}

	return &cfg, nil
}

// ResolveAccount finds the account config for the given AWS account ID.
func (c *ProjectConfig) ResolveAccount(accountID string) (*types.AccountConfig, error) {
	acct, ok := c.Accounts[accountID]
	if !ok {
		return nil, fmt.Errorf("account ID %s not found in .meimei.yaml", accountID)
	}
	return &acct, nil
}
