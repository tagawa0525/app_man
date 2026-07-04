package config_test

import (
	"os"
	"path/filepath"
	"reflect"
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
		Auth: config.AuthConfig{
			// 未指定 (= 0) → validate で 8h デフォルトに補完される
			SessionMaxAgeHours: 8,
		},
		FileStore: config.FileStoreConfig{
			// 未指定 → validate で 20 MiB / PDF・PNG・JPEG に補完される
			UploadMaxBytes:   20971520,
			AllowedMimeTypes: []string{"application/pdf", "image/png", "image/jpeg"},
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
			if !reflect.DeepEqual(got, tt.want) {
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

func TestLoad_envExpansion(t *testing.T) {
	const envName = "TEST_APP_MAN_SESSION_SECRET"
	const envValue = "supersecret-from-env"
	t.Setenv(envName, envValue)

	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
  session_secret_env: ` + envName + `

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

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Server.SessionSecret != envValue {
		t.Errorf("Server.SessionSecret = %q, want %q (env-expanded)", got.Server.SessionSecret, envValue)
	}
}

func TestLoad_envExpansion_nonScalarValue(t *testing.T) {
	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
  session_secret_env:
    - LIST_INSTEAD_OF_ENV_NAME

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

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for non-scalar *_env value, got nil")
	}
}

func TestLoad_envExpansion_emptyEnvName(t *testing.T) {
	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
  session_secret_env: ""

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

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for empty *_env value, got nil")
	}
}

func TestLoad_envExpansion_missingEnv(t *testing.T) {
	const envName = "TEST_APP_MAN_UNSET_SECRET_XYZ"
	if err := os.Unsetenv(envName); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
  session_secret_env: ` + envName + `

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

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error when env var is unset for *_env key, got nil")
	}
}

func TestLoad_authSessionMaxAge_explicit(t *testing.T) {
	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
  cookie_secure: true

database:
  path: ./data/app.db
  wal: true

locks:
  base_dir: ./data/locks

logging:
  level: info
  base_dir: ./logs
  format: json

auth:
  session_max_age_hours: 24
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Auth.SessionMaxAgeHours != 24 {
		t.Errorf("Auth.SessionMaxAgeHours = %d, want 24 (from YAML)", got.Auth.SessionMaxAgeHours)
	}
	if !got.Server.CookieSecure {
		t.Error("Server.CookieSecure = false, want true (from YAML)")
	}
}

func TestLoad_backup_explicit(t *testing.T) {
	yamlBody := `server:
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

backup:
  output_dir: ./data/backups
  generations: 14
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Backup.OutputDir != "./data/backups" {
		t.Errorf("Backup.OutputDir = %q, want %q (from YAML)", got.Backup.OutputDir, "./data/backups")
	}
	if got.Backup.Generations != 14 {
		t.Errorf("Backup.Generations = %d, want 14 (from YAML)", got.Backup.Generations)
	}
}

func TestLoad_backup_unspecifiedDefaultsToZero(t *testing.T) {
	yamlBody := `server:
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

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Backup != (config.BackupConfig{}) {
		t.Errorf("Backup = %+v, want zero value (backup section unspecified)", got.Backup)
	}
}

func TestLoad_backup_negativeGenerationsRejected(t *testing.T) {
	yamlBody := `server:
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

backup:
  output_dir: ./data/backups
  generations: -1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for negative backup.generations, got nil")
	}
}

func TestLoad_fileStore_explicit(t *testing.T) {
	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180

database:
  path: ./data/app.db
  wal: true

file_store:
  base_path: ./data/files
  upload_max_bytes: 1048576
  allowed_mime_types:
    - application/pdf

locks:
  base_dir: ./data/locks

logging:
  level: info
  base_dir: ./logs
  format: json
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	want := config.FileStoreConfig{
		BasePath:         "./data/files",
		UploadMaxBytes:   1048576,
		AllowedMimeTypes: []string{"application/pdf"},
	}
	if !reflect.DeepEqual(got.FileStore, want) {
		t.Errorf("FileStore = %+v, want %+v (from YAML)", got.FileStore, want)
	}
}

func TestLoad_fileStore_negativeUploadMaxBytesRejected(t *testing.T) {
	yamlBody := `server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180

database:
  path: ./data/app.db
  wal: true

file_store:
  upload_max_bytes: -1

locks:
  base_dir: ./data/locks

logging:
  level: info
  base_dir: ./logs
  format: json
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for negative file_store.upload_max_bytes, got nil")
	}
}

func TestLoad_authSessionMaxAge_negativeRejected(t *testing.T) {
	yamlBody := `server:
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

auth:
  session_max_age_hours: -1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() expected error for negative auth.session_max_age_hours, got nil")
	}
}
