// Package notify は通知チャネル (SMTP / Teams Webhook / ファイル出力 /
// 同報) を抽象化する。仕様 §5.9。すべて標準ライブラリのみで実装する。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/config"
)

// Notification は 1 通の通知。ID は notifications テーブルのレコード ID
// (送信前に必ずレコードを作る仕様のため、送信時点で常に存在する)。
type Notification struct {
	ID        int64
	Recipient string
	Subject   string
	Body      string
}

// Notifier は通知チャネルの抽象。仕様 §5.9 の IF。
type Notifier interface {
	Send(ctx context.Context, msg Notification) error
}

// FileNotifier は開発・テスト用のファイル出力チャネル。
// <OutputDir>/<YYYYMMDD-HHMMSS>-<notification_id>.txt に宛先・件名・本文を
// 書き出す。OutputDir が無ければ作成する。
type FileNotifier struct {
	OutputDir string
}

func (n *FileNotifier) Send(_ context.Context, msg Notification) error {
	if err := os.MkdirAll(n.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create notify output dir %s: %w", n.OutputDir, err)
	}
	name := fmt.Sprintf("%s-%d.txt", time.Now().Format("20060102-150405"), msg.ID)
	path := filepath.Join(n.OutputDir, name)
	content := fmt.Sprintf("To: %s\nSubject: %s\n\n%s\n", msg.Recipient, msg.Subject, msg.Body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write notification file %s: %w", path, err)
	}
	return nil
}

// TeamsWebhookNotifier は Teams Incoming Webhook チャネル。件名と本文を
// 連結したテキストを {"text": ...} の JSON で POST する。
type TeamsWebhookNotifier struct {
	WebhookURL string
	// Client が nil なら http.DefaultClient を使う (テストで差し替え可能に)。
	Client *http.Client
}

func (n *TeamsWebhookNotifier) Send(ctx context.Context, msg Notification) error {
	payload := struct {
		Text string `json:"text"`
	}{Text: msg.Subject + "\n\n" + msg.Body}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal teams payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build teams webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post teams webhook: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // 読み捨てクローズの失敗は送信結果に影響しない

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("teams webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// SMTPNotifier は SMTP チャネル。AUTH なし平文で送信する (社内リレー
// 前提。仕様 §10 の設定例にも認証項目は無い)。
type SMTPNotifier struct {
	Host string
	Port int
	From string
}

func (n *SMTPNotifier) Send(ctx context.Context, msg Notification) error {
	// net/smtp は context 非対応のため、少なくとも開始前のキャンセルは
	// 尊重する。
	if err := ctx.Err(); err != nil {
		return err
	}
	addr := net.JoinHostPort(n.Host, strconv.Itoa(n.Port))
	if err := smtp.SendMail(addr, nil, n.From, []string{msg.Recipient}, buildMailMessage(n.From, msg)); err != nil {
		return fmt.Errorf("send mail via %s: %w", addr, err)
	}
	return nil
}

// buildMailMessage は RFC 5322 形式のメールを組み立てる。本文は UTF-8
// プレーンテキスト、件名は非 ASCII を含み得るため Q エンコードする。
func buildMailMessage(from string, msg Notification) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.Recipient)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}

// NamedNotifier は Multi の 1 チャネル。Name はエラーメッセージで失敗
// チャネルを特定するために持つ。
type NamedNotifier struct {
	Name     string
	Notifier Notifier
}

// MultiNotifier は複数チャネルへの同報。1 つが失敗しても残り全チャネルへ
// 送信を試み、失敗をまとめて返す (部分成功でもエラー)。
type MultiNotifier struct {
	Channels []NamedNotifier
}

func (n *MultiNotifier) Send(ctx context.Context, msg Notification) error {
	var errs []error
	for _, ch := range n.Channels {
		if err := ch.Notifier.Send(ctx, msg); err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", ch.Name, err))
		}
	}
	return errors.Join(errs...)
}

// FromConfig は NotifierConfig から Notifier を組み立てる。
//
// mode=off (および空) は (nil, nil) を返す — off は「チャネル不在」であり、
// 呼び出し側 (cmd/notify) が nil を見て検出ごとスキップする設計。静かに
// 成功する NoopNotifier では誤送信フローが sent 記録を残してしまうため、
// 誤って Send された場合に nil パニックとして顕在化する方を安全側とする。
//
// 必須項目の検証は config.Load 側で済んでいる前提 (ここでは組み立てのみ)。
func FromConfig(cfg config.NotifierConfig) (Notifier, error) {
	switch cfg.Mode {
	case "", "off":
		return nil, nil
	case "smtp", "teams", "file":
		return newChannel(cfg.Mode, cfg)
	case "multi":
		if len(cfg.Multi.Channels) == 0 {
			return nil, fmt.Errorf("notifier.multi.channels is empty")
		}
		multi := &MultiNotifier{}
		for _, name := range cfg.Multi.Channels {
			ch, err := newChannel(name, cfg)
			if err != nil {
				return nil, err
			}
			multi.Channels = append(multi.Channels, NamedNotifier{Name: name, Notifier: ch})
		}
		return multi, nil
	default:
		return nil, fmt.Errorf("unknown notifier mode %q", cfg.Mode)
	}
}

// newChannel は単一チャネル (smtp / teams / file) を組み立てる。
func newChannel(name string, cfg config.NotifierConfig) (Notifier, error) {
	switch name {
	case "smtp":
		return &SMTPNotifier{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, From: cfg.SMTP.From}, nil
	case "teams":
		return &TeamsWebhookNotifier{WebhookURL: cfg.Teams.WebhookURL}, nil
	case "file":
		return &FileNotifier{OutputDir: cfg.File.OutputDir}, nil
	default:
		return nil, fmt.Errorf("unknown notifier channel %q", name)
	}
}
