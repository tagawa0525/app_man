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
		Notifier: config.NotifierConfig{
			// 未指定 → validate で off / [30, 90] に補完される
			Mode:             "off",
			ExpiryDaysBefore: []int{30, 90},
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

// notifierTestBase は notifier テスト用の必須最小 YAML。各テストで
// notifier セクションだけ差し替えて使う。
const notifierTestBase = `server:
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

func loadConfigFromYAML(t *testing.T, yamlBody string) (*config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return config.Load(path)
}

func TestLoad_notifier_smtpExplicit(t *testing.T) {
	yamlBody := notifierTestBase + `
notifier:
  mode: smtp
  smtp:
    host: smtp.example.local
    from: app-manager@example.com
  expiry_days_before: [7, 30]
`
	got, err := loadConfigFromYAML(t, yamlBody)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	want := config.NotifierConfig{
		Mode: "smtp",
		SMTP: config.NotifierSMTPConfig{
			Host: "smtp.example.local",
			Port: 25, // 未指定 → validate で 25 に補完される
			From: "app-manager@example.com",
		},
		ExpiryDaysBefore: []int{7, 30},
	}
	if !reflect.DeepEqual(got.Notifier, want) {
		t.Errorf("Notifier = %+v, want %+v", got.Notifier, want)
	}
}

func TestLoad_notifier_multiExplicit(t *testing.T) {
	yamlBody := notifierTestBase + `
notifier:
  mode: multi
  smtp:
    host: smtp.example.local
    port: 587
    from: app-manager@example.com
  file:
    output_dir: ./data/files/mail-out
  multi:
    channels: [smtp, file]
`
	got, err := loadConfigFromYAML(t, yamlBody)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Notifier.Mode != "multi" {
		t.Errorf("Notifier.Mode = %q, want %q", got.Notifier.Mode, "multi")
	}
	if !reflect.DeepEqual(got.Notifier.Multi.Channels, []string{"smtp", "file"}) {
		t.Errorf("Notifier.Multi.Channels = %v, want [smtp file]", got.Notifier.Multi.Channels)
	}
	if got.Notifier.SMTP.Port != 587 {
		t.Errorf("Notifier.SMTP.Port = %d, want 587 (from YAML)", got.Notifier.SMTP.Port)
	}
}

func TestLoad_notifier_webhookURLEnvExpansion(t *testing.T) {
	const envName = "TEST_APP_MAN_TEAMS_WEBHOOK_URL"
	const envValue = "https://example.webhook.office.com/webhookb2/xyz"
	t.Setenv(envName, envValue)

	yamlBody := notifierTestBase + `
notifier:
  mode: teams
  teams:
    webhook_url_env: ` + envName + `
`
	got, err := loadConfigFromYAML(t, yamlBody)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Notifier.Teams.WebhookURL != envValue {
		t.Errorf("Notifier.Teams.WebhookURL = %q, want %q (env-expanded)", got.Notifier.Teams.WebhookURL, envValue)
	}
}

// TestLoad_notifier_unusedChannelNotValidated はチャネル固有の必須項目が
// 「そのモードで使われるときだけ」検査されることを確認する。file モード
// なら smtp / teams の設定が空でも通る。
func TestLoad_notifier_unusedChannelNotValidated(t *testing.T) {
	yamlBody := notifierTestBase + `
notifier:
  mode: file
  file:
    output_dir: ./data/files/mail-out
`
	got, err := loadConfigFromYAML(t, yamlBody)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.Notifier.File.OutputDir != "./data/files/mail-out" {
		t.Errorf("Notifier.File.OutputDir = %q, want %q", got.Notifier.File.OutputDir, "./data/files/mail-out")
	}
	// smtp 未使用なので port の既定値補完もされない
	if got.Notifier.SMTP.Port != 0 {
		t.Errorf("Notifier.SMTP.Port = %d, want 0 (smtp unused)", got.Notifier.SMTP.Port)
	}
}

func TestLoad_notifier_invalidRejected(t *testing.T) {
	tests := []struct {
		name     string
		notifier string
	}{
		{
			name: "unknown mode",
			notifier: `
notifier:
  mode: pigeon
`,
		},
		{
			name: "smtp mode missing host",
			notifier: `
notifier:
  mode: smtp
  smtp:
    from: app-manager@example.com
`,
		},
		{
			name: "smtp mode missing from",
			notifier: `
notifier:
  mode: smtp
  smtp:
    host: smtp.example.local
`,
		},
		{
			name: "smtp port out of range",
			notifier: `
notifier:
  mode: smtp
  smtp:
    host: smtp.example.local
    port: 65536
    from: app-manager@example.com
`,
		},
		{
			name: "teams mode missing webhook_url",
			notifier: `
notifier:
  mode: teams
`,
		},
		{
			name: "file mode missing output_dir",
			notifier: `
notifier:
  mode: file
`,
		},
		{
			name: "multi mode with empty channels",
			notifier: `
notifier:
  mode: multi
`,
		},
		{
			name: "multi channels with unknown channel",
			notifier: `
notifier:
  mode: multi
  multi:
    channels: [file, pigeon]
  file:
    output_dir: ./data/files/mail-out
`,
		},
		{
			// 同一 days の重複は無駄な検出ループと would_send の重複計上になる
			name: "expiry_days_before with duplicates",
			notifier: `
notifier:
  mode: file
  file:
    output_dir: ./data/files/mail-out
  expiry_days_before: [30, 30]
`,
		},
		{
			// 同一チャネルの重複は同じ通知の二重送信になるため拒否する
			name: "multi channels with duplicates",
			notifier: `
notifier:
  mode: multi
  multi:
    channels: [file, file]
  file:
    output_dir: ./data/files/mail-out
`,
		},
		{
			name: "multi channels must not nest multi",
			notifier: `
notifier:
  mode: multi
  multi:
    channels: [multi]
`,
		},
		{
			name: "multi channel smtp missing host",
			notifier: `
notifier:
  mode: multi
  multi:
    channels: [smtp]
  smtp:
    from: app-manager@example.com
`,
		},
		{
			name: "zero expiry_days_before",
			notifier: `
notifier:
  mode: "off"
  expiry_days_before: [0]
`,
		},
		{
			name: "negative expiry_days_before",
			notifier: `
notifier:
  mode: "off"
  expiry_days_before: [30, -1]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfigFromYAML(t, notifierTestBase+tt.notifier)
			if err == nil {
				t.Fatal("Load() expected error, got nil")
			}
		})
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
