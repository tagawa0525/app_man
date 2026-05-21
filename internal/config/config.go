package config

import (
	"fmt"
	"os"

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

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
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
