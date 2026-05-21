package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/config"
)

func TestLoad(t *testing.T) {
	validYAML := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180

database:
  path: ./data/app.db
  wal: true

locks:
  base_dir: ./data/locks

logging:
  level: info
  base_dir: ./logs
  format: json
`

	want := &config.Config{
		Server: config.ServerConfig{
			Listen:  "0.0.0.0:8180",
			BaseURL: "http://localhost:8180",
		},
		Database: config.DatabaseConfig{
			Path: "./data/app.db",
			WAL:  true,
		},
		Locks: config.LocksConfig{
			BaseDir: "./data/locks",
		},
		Logging: config.LoggingConfig{
			Level:   "info",
			BaseDir: "./logs",
			Format:  "json",
		},
	}

	tests := []struct {
		name    string
		yaml    string
		want    *config.Config
		wantErr bool
	}{
		{
			name: "valid minimal config",
			yaml: validYAML,
			want: want,
		},
		{
			name:    "missing required server.listen",
			yaml:    "server:\n  base_url: http://localhost:8180\n",
			wantErr: true,
		},
		{
			name:    "missing required database.path",
			yaml:    "server:\n  listen: 0.0.0.0:8180\n  base_url: http://localhost:8180\n",
			wantErr: true,
		},
		{
			name:    "invalid YAML",
			yaml:    "server:\n  listen: [unterminated\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatalf("write temp config: %v", err)
			}

			got, err := config.Load(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got == nil {
				t.Fatal("Load() returned nil config without error")
			}
			if *got != *tt.want {
				t.Errorf("Load() = %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

func TestLoad_nonExistentFile(t *testing.T) {
	_, err := config.Load("/no/such/file/config.yml")
	if err == nil {
		t.Fatal("Load() expected error for non-existent file, got nil")
	}
}
