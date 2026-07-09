package repository_test

import (
	"context"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// notifications_test.go は gave_up サマリの当日 dedup 判定を repository
// レベルで固定する (PR #37 Copilot 指摘)。

// CountNotificationsByKindOnOrAfterDay は保存形式の違い (CURRENT_TIMESTAMP
// の "YYYY-MM-DD HH:MM:SS" と Go ドライバの "…+0000 UTC") に依存せず、
// 深夜 0 時ちょうどに作成された行も当日分として数える。time.Time を
// そのまま bind すると文字列比較の形式差で 00:00:00 の行が漏れる。
func TestCountNotificationsByKindOnOrAfterDay_MidnightRow(t *testing.T) {
	t.Parallel()
	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()

	// CURRENT_TIMESTAMP 形式で「今日の 00:00:00 ちょうど」の行を作る。
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, status, created_at)
		VALUES ('gave_up_summary', 'file', 'a@x', 'sent', date('now') || ' 00:00:00')`); err != nil {
		t.Fatalf("insert midnight summary: %v", err)
	}
	// 前日の行は数えない。
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, status, created_at)
		VALUES ('gave_up_summary', 'file', 'a@x', 'sent', date('now', '-1 day') || ' 23:59:59')`); err != nil {
		t.Fatalf("insert yesterday summary: %v", err)
	}

	var today string
	if err := sqlDB.QueryRow(`SELECT date('now')`).Scan(&today); err != nil {
		t.Fatalf("date('now'): %v", err)
	}
	n, err := q.CountNotificationsByKindOnOrAfterDay(ctx, repository.CountNotificationsByKindOnOrAfterDayParams{
		Kind: "gave_up_summary",
		Day:  today,
	})
	if err != nil {
		t.Fatalf("CountNotificationsByKindOnOrAfterDay: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1 (midnight row counted, yesterday excluded)", n)
	}
}
