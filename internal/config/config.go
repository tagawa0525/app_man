package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Locks    LocksConfig    `yaml:"locks"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Listen        string `yaml:"listen"`
	BaseURL       string `yaml:"base_url"`
	SessionSecret string `yaml:"session_secret"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
	WAL  bool   `yaml:"wal"`
}

type LocksConfig struct {
	BaseDir string `yaml:"base_dir"`
}

type LoggingConfig struct {
	Level   string `yaml:"level"`
	BaseDir string `yaml:"base_dir"`
	Format  string `yaml:"format"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := resolveEnvKeys(&root); err != nil {
		return nil, fmt.Errorf("resolve env in %s: %w", path, err)
	}

	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	if c.Locks.BaseDir == "" {
		return fmt.Errorf("locks.base_dir is required")
	}
	if c.Logging.BaseDir == "" {
		return fmt.Errorf("logging.base_dir is required")
	}
	return nil
}

// resolveEnvKeys walks a YAML node tree and rewrites any mapping key ending
// in "_env" by removing the suffix and replacing the value with the contents
// of the referenced environment variable. Returns an error if any referenced
// variable is not set.
func resolveEnvKeys(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := resolveEnvKeys(c); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		if len(n.Content)%2 != 0 {
			return fmt.Errorf("malformed YAML mapping at line %d: odd number of key/value nodes", n.Line)
		}
		newContent := make([]*yaml.Node, 0, len(n.Content))
		for i := 0; i < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]

			if keyNode.Kind == yaml.ScalarNode && strings.HasSuffix(keyNode.Value, "_env") && len(keyNode.Value) > len("_env") {
				if valNode.Kind != yaml.ScalarNode {
					return fmt.Errorf("key %s at line %d expects a scalar environment variable name, got non-scalar value", keyNode.Value, keyNode.Line)
				}
				envName := valNode.Value
				if envName == "" {
					return fmt.Errorf("key %s at line %d has empty environment variable name", keyNode.Value, keyNode.Line)
				}
				envVal, ok := os.LookupEnv(envName)
				if !ok {
					return fmt.Errorf("environment variable %s referenced by key %s is not set", envName, keyNode.Value)
				}
				resolvedKey := strings.TrimSuffix(keyNode.Value, "_env")
				newContent = append(newContent,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: resolvedKey},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: envVal},
				)
				continue
			}

			if err := resolveEnvKeys(valNode); err != nil {
				return err
			}
			newContent = append(newContent, keyNode, valNode)
		}
		n.Content = newContent
	}
	return nil
}
