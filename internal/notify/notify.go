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
		// parse エラー等の Error() にも URL が含まれるため redact する
		return fmt.Errorf("build teams webhook request: %s", redactURL(err.Error(), n.WebhookURL))
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.Client
	if client == nil {
		// http.DefaultClient は Timeout を持たず、ネットワークの詰まりで
		// バッチ全体 (と lock) が無期限に止まりうる。既定は有限にする
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error 等は URL (機微情報) を含むため、エラー文字列から
		// 確実に取り除く。%w で包むと Error() 経由で漏れるので包まない
		return fmt.Errorf("post teams webhook: %s", redactURL(err.Error(), n.WebhookURL))
	}
	defer resp.Body.Close() //nolint:errcheck // 読み捨てクローズの失敗は送信結果に影響しない

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("teams webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// redactURL は err メッセージ中の webhook URL を伏字にする。
// notifications.last_error・ログ・gave_up サマリ本文に流れても
// 機微情報 (URL 内のトークン) が漏れないようにするため。
func redactURL(msg, url string) string {
	if url == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, url, "[redacted webhook URL]")
	// url.Error / parse エラーは URL を %q (エスケープ付きクォート) で
	// 含むため、その形も置換する。
	return strings.ReplaceAll(msg, strconv.Quote(url), `"[redacted webhook URL]"`)
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
	// SMTP ヘッダインジェクション対策: CR/LF 混入を接続前に拒否する。
	// net/smtp も from / to は検証し、Subject は Q エンコードで実害を
	// 防げるが、3 フィールドとも本実装の責務として決定論的なエラーで
	// 弾く (呼び出し側が failed + last_error として記録する)。
	if err := rejectCRLF("from", n.From); err != nil {
		return err
	}
	if err := rejectCRLF("recipient", msg.Recipient); err != nil {
		return err
	}
	if err := rejectCRLF("subject", msg.Subject); err != nil {
		return err
	}
	addr := net.JoinHostPort(n.Host, strconv.Itoa(n.Port))
	if err := smtp.SendMail(addr, nil, n.From, []string{msg.Recipient}, buildMailMessage(n.From, msg)); err != nil {
		return fmt.Errorf("send mail via %s: %w", addr, err)
	}
	return nil
}

// rejectCRLF は SMTP ヘッダへ改行で別ヘッダを注入される攻撃 (header
// injection) を防ぐため、ヘッダに入る値の CR/LF を拒否する。
func rejectCRLF(field, v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return fmt.Errorf("smtp header %s must not contain CR/LF (header injection): %q", field, v)
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
	// 既存の CRLF を LF に正規化してから CRLF へ変換する
	// (そのまま置換すると \r\r\n に壊れる)
	body := strings.ReplaceAll(msg.Body, "\r\n", "\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}

// NamedNotifier は名前付きの 1 チャネル。Name は notifications.channel への
// 記録とエラーメッセージでの失敗チャネル特定に使う。
type NamedNotifier struct {
	Name     string
	Notifier Notifier
}

// MultiNotifier は複数チャネルへの同報。1 つが失敗しても残り全チャネルへ
// 送信を試み、失敗をまとめて返す (部分成功でもエラー)。
//
// cmd/notify はチャネル別レコード方式 (FromConfig のチャネル列を展開して
// チャネルごとに notifications レコードを作る) のため本型を使わないが、
// 仕様 §5.9 が列挙する Notifier IF の実装として提供を続ける。
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

// FromConfig は NotifierConfig から (チャネル名, Notifier) の列を組み立てる。
// 単一モード (smtp / teams / file) は 1 要素、multi は channels を設定順に
// 展開、mode=off (および空) は空を返す — off は「チャネル不在」であり、
// 呼び出し側 (cmd/notify) が空を見て検出ごとスキップする設計。
//
// チャネル列で返すのは、呼び出し側が宛先 × チャネルごとに notifications
// レコードを作成して個別に送信できるようにするため。multi の部分成功時に
// 成功チャネルは sent / 失敗チャネルだけ failed になり、--retry-failed は
// 失敗チャネルのみを再送できる (MultiNotifier での同報 1 レコードだと部分
// 成功の再送で成功済みチャネルへ重複送信される)。
//
// 必須項目の検証は config.Load 側で済んでいる前提 (ここでは組み立てのみ)。
func FromConfig(cfg config.NotifierConfig) ([]NamedNotifier, error) {
	switch cfg.Mode {
	case "", "off":
		return nil, nil
	case "smtp", "teams", "file":
		ch, err := newChannel(cfg.Mode, cfg)
		if err != nil {
			return nil, err
		}
		return []NamedNotifier{{Name: cfg.Mode, Notifier: ch}}, nil
	case "multi":
		if len(cfg.Multi.Channels) == 0 {
			return nil, fmt.Errorf("notifier.multi.channels is empty")
		}
		channels := make([]NamedNotifier, 0, len(cfg.Multi.Channels))
		for _, name := range cfg.Multi.Channels {
			ch, err := newChannel(name, cfg)
			if err != nil {
				return nil, err
			}
			channels = append(channels, NamedNotifier{Name: name, Notifier: ch})
		}
		return channels, nil
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
