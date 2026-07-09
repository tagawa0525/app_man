package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	FileStore FileStoreConfig `yaml:"file_store"`
	Locks     LocksConfig     `yaml:"locks"`
	Logging   LoggingConfig   `yaml:"logging"`
	Auth      AuthConfig      `yaml:"auth"`
	Backup    BackupConfig    `yaml:"backup"`
	Notifier  NotifierConfig  `yaml:"notifier"`
}

type ServerConfig struct {
	Listen        string `yaml:"listen"`
	BaseURL       string `yaml:"base_url"`
	SessionSecret string `yaml:"session_secret"`
	// CookieSecure は Cookie の Secure 属性を立てるかどうか。本番 (HTTPS) で
	// true、開発 (HTTP) で false にする。仕様書 §7.3 で SameSite=Lax /
	// HttpOnly は固定だが、Secure はトランスポート前提で切り替えが必要。
	CookieSecure bool `yaml:"cookie_secure"`
}

// AuthConfig は認証・セッション関連の設定。
// 後続 PR (Authenticator) で Mode / LDAP / RemoteHeader フィールドを足す予定。
type AuthConfig struct {
	// SessionMaxAgeHours はセッション Cookie / DB レコードの有効期間 (時間)。
	// 仕様書 §7.3 のサンプル値は 8。未指定 (= 0) なら 8 にフォールバック、
	// 負値はエラー。
	SessionMaxAgeHours int `yaml:"session_max_age_hours"`
}

// FileStoreConfig はファイルストア (証書ファイル等の物理配置) の設定。
type FileStoreConfig struct {
	// BasePath は仕様 §3.2 のレイアウトのルート (<base>)。appmgr-server
	// 起動時に必須。バッチ系バイナリは file_store を使わないため、validate
	// では必須チェックせず消費者側 (server) で検査する (backup.output_dir
	// と同じ配置)。
	BasePath string `yaml:"base_path"`
	// UploadMaxBytes はアップロードのサイズ上限 (バイト)。仕様 §10 の
	// サンプル値は 20971520 (20 MiB)。未指定 (= 0) なら 20971520 に
	// フォールバック、負値はエラー。
	UploadMaxBytes int64 `yaml:"upload_max_bytes"`
	// AllowedMimeTypes は許可する MIME タイプ (仕様 §8.3。マジックバイト
	// 判定の結果と照合する)。未指定なら application/pdf / image/png /
	// image/jpeg にフォールバック。
	AllowedMimeTypes []string `yaml:"allowed_mime_types"`
}

// BackupConfig は appmgr-backup の設定。
type BackupConfig struct {
	OutputDir   string `yaml:"output_dir"`  // VACUUM INTO の出力先。appmgr-backup 実行時に必須
	Generations int    `yaml:"generations"` // 保持世代数。0 = 無制限、負値はエラー
}

// NotifierConfig は通知チャネル (appmgr-notify) の設定。仕様 §5.9 / §10。
// mode が off (または未指定) のとき通知バッチは no-op になる。チャネル固有の
// 必須項目 (host / from / webhook_url / output_dir) は「そのモードで実際に
// 使われるときだけ」検査する — file モードだけ使う環境で smtp 設定を
// 要求しないため。
type NotifierConfig struct {
	// Mode は smtp / teams / file / multi / off。未指定 (空) は off に正規化。
	Mode  string              `yaml:"mode"`
	SMTP  NotifierSMTPConfig  `yaml:"smtp"`
	Teams NotifierTeamsConfig `yaml:"teams"`
	File  NotifierFileConfig  `yaml:"file"`
	Multi NotifierMultiConfig `yaml:"multi"`
	// ExpiryDaysBefore はライセンス満了通知を出す「満了 N 日前」の一覧。
	// 正整数のみ。未指定なら [30, 90] にフォールバック (仕様 §10 の例)。
	ExpiryDaysBefore []int `yaml:"expiry_days_before"`
}

// NotifierSMTPConfig は SMTP チャネルの設定。AUTH なし平文 (社内リレー前提)。
type NotifierSMTPConfig struct {
	Host string `yaml:"host"` // smtp 使用時に必須
	Port int    `yaml:"port"` // 未指定 (= 0) なら 25 にフォールバック
	From string `yaml:"from"` // smtp 使用時に必須
}

// NotifierTeamsConfig は Teams Incoming Webhook チャネルの設定。
type NotifierTeamsConfig struct {
	// WebhookURL は機微情報のため通常 webhook_url_env で環境変数から
	// 解決する (resolveEnvKeys が自動処理)。teams 使用時に必須。
	WebhookURL string `yaml:"webhook_url"`
}

// NotifierFileConfig はファイル出力チャネル (開発・テスト用) の設定。
type NotifierFileConfig struct {
	OutputDir string `yaml:"output_dir"` // file 使用時に必須
}

// NotifierMultiConfig は同報 (multi) モードの設定。
type NotifierMultiConfig struct {
	// Channels は同報先チャネル名の一覧。smtp / teams / file のみ許可
	// (multi の入れ子や off は不可)。mode=multi のとき必須。
	Channels []string `yaml:"channels"`
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
	if c.Auth.SessionMaxAgeHours < 0 {
		return fmt.Errorf("auth.session_max_age_hours must be >= 0 (0 means default 8h)")
	}
	if c.Auth.SessionMaxAgeHours == 0 {
		c.Auth.SessionMaxAgeHours = 8
	}
	if c.Backup.Generations < 0 {
		return fmt.Errorf("backup.generations must be >= 0 (0 means keep all)")
	}
	if c.FileStore.UploadMaxBytes < 0 {
		return fmt.Errorf("file_store.upload_max_bytes must be >= 0 (0 means default 20971520)")
	}
	if c.FileStore.UploadMaxBytes == 0 {
		c.FileStore.UploadMaxBytes = 20971520
	}
	if len(c.FileStore.AllowedMimeTypes) == 0 {
		c.FileStore.AllowedMimeTypes = []string{"application/pdf", "image/png", "image/jpeg"}
	}
	return c.validateNotifier()
}

func (c *Config) validateNotifier() error {
	n := &c.Notifier

	switch n.Mode {
	case "":
		n.Mode = "off"
	case "smtp", "teams", "file", "multi", "off":
	default:
		return fmt.Errorf("notifier.mode must be one of smtp/teams/file/multi/off, got %q", n.Mode)
	}

	// used は各チャネルが実際に使われるかどうか。必須項目の検査は
	// 使われるチャネルに限定する。
	used := map[string]bool{}
	switch n.Mode {
	case "smtp", "teams", "file":
		used[n.Mode] = true
	case "multi":
		if len(n.Multi.Channels) == 0 {
			return fmt.Errorf("notifier.multi.channels is required when notifier.mode is multi")
		}
		for _, ch := range n.Multi.Channels {
			switch ch {
			case "smtp", "teams", "file":
				// 重複は同じ通知の二重送信になるため拒否する。
				if used[ch] {
					return fmt.Errorf("notifier.multi.channels must not contain duplicates, got %q twice", ch)
				}
				used[ch] = true
			default:
				return fmt.Errorf("notifier.multi.channels must contain only smtp/teams/file, got %q", ch)
			}
		}
	}

	if used["smtp"] {
		if n.SMTP.Host == "" {
			return fmt.Errorf("notifier.smtp.host is required when the smtp channel is used")
		}
		if n.SMTP.From == "" {
			return fmt.Errorf("notifier.smtp.from is required when the smtp channel is used")
		}
		if n.SMTP.Port < 0 || n.SMTP.Port > 65535 {
			return fmt.Errorf("notifier.smtp.port must be in 0-65535 (0 means default 25), got %d", n.SMTP.Port)
		}
		if n.SMTP.Port == 0 {
			n.SMTP.Port = 25
		}
	}
	if used["teams"] && n.Teams.WebhookURL == "" {
		return fmt.Errorf("notifier.teams.webhook_url (or webhook_url_env) is required when the teams channel is used")
	}
	if used["file"] && n.File.OutputDir == "" {
		return fmt.Errorf("notifier.file.output_dir is required when the file channel is used")
	}

	seenDays := make(map[int]bool, len(n.ExpiryDaysBefore))
	for _, d := range n.ExpiryDaysBefore {
		if d <= 0 {
			return fmt.Errorf("notifier.expiry_days_before must contain only positive integers, got %d", d)
		}
		// 重複は無駄な検出ループと dry-run の would_send 重複計上になる
		if seenDays[d] {
			return fmt.Errorf("notifier.expiry_days_before must not contain duplicates, got %d twice", d)
		}
		seenDays[d] = true
	}
	if len(n.ExpiryDaysBefore) == 0 {
		n.ExpiryDaysBefore = []int{30, 90}
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
