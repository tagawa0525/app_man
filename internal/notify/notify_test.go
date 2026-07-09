package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/notify"
)

// fakeNotifier は Multi のテスト用。受信した Notification を記録し、
// 固定エラーを返せる。
type fakeNotifier struct {
	sent []notify.Notification
	err  error
}

func (f *fakeNotifier) Send(_ context.Context, msg notify.Notification) error {
	f.sent = append(f.sent, msg)
	return f.err
}

// TestFileNotifier_writesNotificationToFile は宛先・件名・本文がファイルに
// 出力されること、output_dir が自動作成されること、ファイル名が
// <YYYYMMDD-HHMMSS>-<notification_id>.txt 形式であることを検証する。
func TestFileNotifier_writesNotificationToFile(t *testing.T) {
	// まだ存在しないサブディレクトリを指定して自動作成を確認する
	dir := filepath.Join(t.TempDir(), "mail-out")
	n := &notify.FileNotifier{OutputDir: dir}

	msg := notify.Notification{
		ID:        42,
		Recipient: "alice@example.com",
		Subject:   "ライセンス満了 30 日前",
		Body:      "本文 1 行目\n本文 2 行目",
	}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("output dir has %d entries, want 1", len(entries))
	}

	name := entries[0].Name()
	if !regexp.MustCompile(`^\d{8}-\d{6}-42\.txt$`).MatchString(name) {
		t.Errorf("file name = %q, want <YYYYMMDD>-<HHMMSS>-42.txt", name)
	}

	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read notification file: %v", err)
	}
	content := string(raw)
	for _, want := range []string{
		"To: alice@example.com",
		"Subject: ライセンス満了 30 日前",
		"本文 1 行目\n本文 2 行目",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("notification file does not contain %q:\n%s", want, content)
		}
	}
}

// TestTeamsWebhookNotifier_postsTextJSON は Webhook URL へ JSON
// ({"text": ...}) が POST され、text に件名と本文が含まれることを検証する。
func TestTeamsWebhookNotifier_postsTextJSON(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotPayload     struct {
			Text string `json:"text"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read webhook request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotPayload); err != nil {
			t.Errorf("webhook request body is not JSON: %v (body %q)", err, body)
		}
	}))
	defer srv.Close()

	n := &notify.TeamsWebhookNotifier{WebhookURL: srv.URL}
	msg := notify.Notification{
		ID:        7,
		Recipient: "channel",
		Subject:   "件名テスト",
		Body:      "本文テスト",
	}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("webhook method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("webhook Content-Type = %q, want application/json", gotContentType)
	}
	if !strings.Contains(gotPayload.Text, "件名テスト") || !strings.Contains(gotPayload.Text, "本文テスト") {
		t.Errorf("webhook text = %q, want subject and body included", gotPayload.Text)
	}
}

// TestTeamsWebhookNotifier_non2xxIsError は非 2xx 応答がエラーになることを
// 検証する (ステータスコードをエラーメッセージに含める)。
func TestTeamsWebhookNotifier_non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := &notify.TeamsWebhookNotifier{WebhookURL: srv.URL}
	err := n.Send(context.Background(), notify.Notification{ID: 1, Recipient: "channel"})
	if err == nil {
		t.Fatal("Send() expected error for non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Send() error = %v, want status code 500 included", err)
	}
}

// TestMultiNotifier_broadcastsToAllChannels は全チャネルへ同報されることを
// 検証する。
func TestMultiNotifier_broadcastsToAllChannels(t *testing.T) {
	a := &fakeNotifier{}
	b := &fakeNotifier{}
	m := &notify.MultiNotifier{Channels: []notify.NamedNotifier{
		{Name: "smtp", Notifier: a},
		{Name: "teams", Notifier: b},
	}}

	msg := notify.Notification{ID: 3, Recipient: "alice@example.com", Subject: "s", Body: "b"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() unexpected error: %v", err)
	}
	if len(a.sent) != 1 || len(b.sent) != 1 {
		t.Fatalf("sent counts = (%d, %d), want (1, 1)", len(a.sent), len(b.sent))
	}
	if a.sent[0] != msg || b.sent[0] != msg {
		t.Errorf("channels received %+v / %+v, want %+v", a.sent[0], b.sent[0], msg)
	}
}

// TestMultiNotifier_partialFailureIsError は片方のチャネルが失敗したとき
// 全体がエラーになり、失敗チャネル名を含むこと、残りのチャネルへは
// 送信が試行されることを検証する。
func TestMultiNotifier_partialFailureIsError(t *testing.T) {
	a := &fakeNotifier{err: errors.New("connection refused")}
	b := &fakeNotifier{}
	m := &notify.MultiNotifier{Channels: []notify.NamedNotifier{
		{Name: "smtp", Notifier: a},
		{Name: "teams", Notifier: b},
	}}

	err := m.Send(context.Background(), notify.Notification{ID: 4, Recipient: "alice@example.com"})
	if err == nil {
		t.Fatal("Send() expected error when one channel fails, got nil")
	}
	if !strings.Contains(err.Error(), "smtp") {
		t.Errorf("Send() error = %v, want failed channel name (smtp) included", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Send() error = %v, want underlying error included", err)
	}
	// 失敗チャネルがあっても他チャネルへの送信は行われる (同報)
	if len(b.sent) != 1 {
		t.Errorf("teams channel sent count = %d, want 1 (broadcast even on failure)", len(b.sent))
	}
}

// TestSMTPNotifier_connectionFailure は接続不能な宛先 (確保してから閉じた
// ポート) への送信がエラーになることを検証する。
func TestSMTPNotifier_connectionFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	n := &notify.SMTPNotifier{Host: host, Port: port, From: "app-manager@example.com"}
	sendErr := n.Send(context.Background(), notify.Notification{
		ID:        5,
		Recipient: "alice@example.com",
		Subject:   "s",
		Body:      "b",
	})
	if sendErr == nil {
		t.Fatal("Send() expected error for closed port, got nil")
	}
}

// TestSMTPNotifier_rejectsHeaderInjection は From / Recipient / Subject に
// CR/LF が混入した Notification を接続前に拒否することを検証する (SMTP
// ヘッダインジェクション対策)。net/smtp も from / to は検証するが、Subject
// を含む 3 フィールドを本実装の責務として決定論的なエラーで弾く (呼び出し
// 側はこれを failed + last_error として記録する)。
func TestSMTPNotifier_rejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name string
		from string
		msg  notify.Notification
	}{
		{
			name: "recipient with CRLF",
			from: "app-manager@example.com",
			msg: notify.Notification{
				ID:        9,
				Recipient: "alice@example.com\r\nBcc: evil@example.com",
				Subject:   "s",
				Body:      "b",
			},
		},
		{
			name: "subject with LF",
			from: "app-manager@example.com",
			msg: notify.Notification{
				ID:        10,
				Recipient: "alice@example.com",
				Subject:   "hello\nBcc: evil@example.com",
				Body:      "b",
			},
		},
		{
			name: "from with CR",
			from: "app-manager@example.com\rBcc: evil@example.com",
			msg: notify.Notification{
				ID:        11,
				Recipient: "alice@example.com",
				Subject:   "s",
				Body:      "b",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 検証で接続前に弾かれるため、到達不能なホストでよい
			// (検証が無いと dial まで進み、別のエラーになる)。
			n := &notify.SMTPNotifier{Host: "smtp.example.invalid", Port: 25, From: tt.from}
			err := n.Send(context.Background(), tt.msg)
			if err == nil {
				t.Fatal("Send() expected error for CR/LF in header field, got nil")
			}
			if !strings.Contains(err.Error(), "header") {
				t.Errorf("Send() error = %v, want header-injection validation error", err)
			}
		})
	}
}

// TestFromConfig は mode ごとに (チャネル名, Notifier) の列が返ることを
// 検証する。単一モードは 1 要素、multi は channels を設定順に展開、
// mode=off は空 — off は「チャネル不在」であり、呼び出し側 (cmd/notify) が
// 空を見て検出ごとスキップする設計。チャネル列で返すのは、runner が宛先 ×
// チャネルごとに notifications レコードを作り、multi の部分成功時に失敗
// チャネルだけを再送できるようにするため (チャネル別レコード方式)。
func TestFromConfig(t *testing.T) {
	t.Run("off returns no channels", func(t *testing.T) {
		for _, mode := range []string{"off", ""} {
			chs, err := notify.FromConfig(config.NotifierConfig{Mode: mode})
			if err != nil {
				t.Fatalf("FromConfig(mode=%q) unexpected error: %v", mode, err)
			}
			if len(chs) != 0 {
				t.Errorf("FromConfig(mode=%q) = %d channels, want 0", mode, len(chs))
			}
		}
	})

	t.Run("file", func(t *testing.T) {
		chs, err := notify.FromConfig(config.NotifierConfig{
			Mode: "file",
			File: config.NotifierFileConfig{OutputDir: "./mail-out"},
		})
		if err != nil {
			t.Fatalf("FromConfig() unexpected error: %v", err)
		}
		if len(chs) != 1 || chs[0].Name != "file" {
			t.Fatalf("FromConfig() = %+v, want single channel named file", chs)
		}
		fn, ok := chs[0].Notifier.(*notify.FileNotifier)
		if !ok {
			t.Fatalf("Notifier = %T, want *notify.FileNotifier", chs[0].Notifier)
		}
		if fn.OutputDir != "./mail-out" {
			t.Errorf("OutputDir = %q, want %q", fn.OutputDir, "./mail-out")
		}
	})

	t.Run("teams", func(t *testing.T) {
		chs, err := notify.FromConfig(config.NotifierConfig{
			Mode:  "teams",
			Teams: config.NotifierTeamsConfig{WebhookURL: "https://example.invalid/hook"},
		})
		if err != nil {
			t.Fatalf("FromConfig() unexpected error: %v", err)
		}
		if len(chs) != 1 || chs[0].Name != "teams" {
			t.Fatalf("FromConfig() = %+v, want single channel named teams", chs)
		}
		tn, ok := chs[0].Notifier.(*notify.TeamsWebhookNotifier)
		if !ok {
			t.Fatalf("Notifier = %T, want *notify.TeamsWebhookNotifier", chs[0].Notifier)
		}
		if tn.WebhookURL != "https://example.invalid/hook" {
			t.Errorf("WebhookURL = %q, want %q", tn.WebhookURL, "https://example.invalid/hook")
		}
	})

	t.Run("smtp", func(t *testing.T) {
		chs, err := notify.FromConfig(config.NotifierConfig{
			Mode: "smtp",
			SMTP: config.NotifierSMTPConfig{Host: "smtp.example.local", Port: 25, From: "app@example.com"},
		})
		if err != nil {
			t.Fatalf("FromConfig() unexpected error: %v", err)
		}
		if len(chs) != 1 || chs[0].Name != "smtp" {
			t.Fatalf("FromConfig() = %+v, want single channel named smtp", chs)
		}
		sn, ok := chs[0].Notifier.(*notify.SMTPNotifier)
		if !ok {
			t.Fatalf("Notifier = %T, want *notify.SMTPNotifier", chs[0].Notifier)
		}
		if sn.Host != "smtp.example.local" || sn.Port != 25 || sn.From != "app@example.com" {
			t.Errorf("SMTPNotifier = %+v, want host/port/from from config", sn)
		}
	})

	t.Run("multi expands channels in config order", func(t *testing.T) {
		chs, err := notify.FromConfig(config.NotifierConfig{
			Mode: "multi",
			SMTP: config.NotifierSMTPConfig{Host: "smtp.example.local", Port: 25, From: "app@example.com"},
			File: config.NotifierFileConfig{OutputDir: "./mail-out"},
			Multi: config.NotifierMultiConfig{
				Channels: []string{"smtp", "file"},
			},
		})
		if err != nil {
			t.Fatalf("FromConfig() unexpected error: %v", err)
		}
		if len(chs) != 2 {
			t.Fatalf("len(channels) = %d, want 2", len(chs))
		}
		if chs[0].Name != "smtp" || chs[1].Name != "file" {
			t.Errorf("channel names = %q, %q, want smtp, file (config order)", chs[0].Name, chs[1].Name)
		}
		if _, ok := chs[0].Notifier.(*notify.SMTPNotifier); !ok {
			t.Errorf("channels[0].Notifier = %T, want *notify.SMTPNotifier", chs[0].Notifier)
		}
		if _, ok := chs[1].Notifier.(*notify.FileNotifier); !ok {
			t.Errorf("channels[1].Notifier = %T, want *notify.FileNotifier", chs[1].Notifier)
		}
	})

	t.Run("unknown mode is error", func(t *testing.T) {
		if _, err := notify.FromConfig(config.NotifierConfig{Mode: "pigeon"}); err == nil {
			t.Fatal("FromConfig() expected error for unknown mode, got nil")
		}
	})

	t.Run("multi with unknown channel is error", func(t *testing.T) {
		_, err := notify.FromConfig(config.NotifierConfig{
			Mode:  "multi",
			Multi: config.NotifierMultiConfig{Channels: []string{"pigeon"}},
		})
		if err == nil {
			t.Fatal("FromConfig() expected error for unknown channel, got nil")
		}
	})
}

// Webhook URL は機微情報のため、接続エラー等のエラー文字列に URL が
// そのまま含まれてはならない (last_error / ログ / サマリ本文へ流れる)。
func TestTeamsWebhookNotifier_redactsURLInErrors(t *testing.T) {
	t.Parallel()
	// 接続先のない URL (クローズ済みポート) で *url.Error を誘発する。
	const secretURL = "http://127.0.0.1:1/hooks/secret-token-xyz"
	n := &notify.TeamsWebhookNotifier{WebhookURL: secretURL}
	err := n.Send(context.Background(), notify.Notification{ID: 1, Recipient: "teams", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("Send to unreachable webhook should fail")
	}
	if strings.Contains(err.Error(), "secret-token-xyz") || strings.Contains(err.Error(), secretURL) {
		t.Errorf("error must not leak webhook URL: %v", err)
	}
}

// リクエスト生成エラー (不正 URL 等) の経路でも webhook URL を漏らさない。
func TestTeamsWebhookNotifier_redactsURLInRequestBuildErrors(t *testing.T) {
	t.Parallel()
	const badURL = "http://127.0.0.1:1/hooks/secret-token-abc\x7f" // 制御文字で NewRequest が失敗する
	n := &notify.TeamsWebhookNotifier{WebhookURL: badURL}
	err := n.Send(context.Background(), notify.Notification{ID: 1, Recipient: "teams", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("Send with invalid URL should fail")
	}
	if strings.Contains(err.Error(), "secret-token-abc") {
		t.Errorf("request-build error must not leak webhook URL: %v", err)
	}
}
