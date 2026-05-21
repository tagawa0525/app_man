package config

import "errors"

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Locks    LocksConfig    `yaml:"locks"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Listen  string `yaml:"listen"`
	BaseURL string `yaml:"base_url"`
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
	return nil, errors.New("not implemented")
}
