package web

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	auditlogview "github.com/tagawa0525/app_man/internal/view/auditlogs"
)

// audit_logs.go は監査ログ閲覧画面 (仕様 §6.1 / Plan admin-audit-logs.md)
// の web 層:
//
//   - GET /admin/audit-logs  一覧 (id 降順 100 件 + 「さらに表示」カーソル)
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。閲覧専用で書込み
// UI は持たない (削除は appmgr-prune-logs の責務)。閲覧自体は audit に
// 記録しない (閲覧記録はノイズ、Plan)。
type auditLogHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// auditLogsPageSize は 1 ページの表示件数。ListAuditLogs は +1 の 101 件
// を取り、101 件目の有無で「さらに表示」の表示を判定する (OFFSET はログ
// 肥大で劣化するため id カーソル方式、Plan)。
const auditLogsPageSize = 100

// auditLogsJST は表示用タイムゾーン。occurred_at は UTC
// (CURRENT_TIMESTAMP) で保存されるため、表示時のみ +9h する。
var auditLogsJST = time.FixedZone("JST", 9*3600)

// list は GET /admin/audit-logs。フィルタ 3 種 (action 前方一致 /
// entity_type 完全一致 / username 完全一致) は空文字なら無条件。
// before_id は id カーソル (0 = 先頭ページ) で、非数値・負は 400。
func (h *auditLogHandlers) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	action := strings.TrimSpace(query.Get("action"))
	entityType := strings.TrimSpace(query.Get("entity_type"))
	username := strings.TrimSpace(query.Get("username"))

	var beforeID int64
	if raw := query.Get("before_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "before_id が不正です", http.StatusBadRequest)
			return
		}
		beforeID = n
	}

	q := repository.New(h.db)
	rows, err := q.ListAuditLogs(r.Context(), repository.ListAuditLogsParams{
		ActionPrefix: action,
		EntityType:   entityType,
		Username:     username,
		BeforeID:     beforeID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list audit logs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasMore := len(rows) > auditLogsPageSize
	if hasMore {
		rows = rows[:auditLogsPageSize]
	}

	props := auditlogview.ListProps{
		Action:     action,
		EntityType: entityType,
		Username:   username,
	}
	for _, row := range rows {
		v := auditlogview.Row{
			OccurredAt: row.OccurredAt.In(auditLogsJST).Format("2006-01-02 15:04:05"),
			Username:   "-",
			Action:     row.Action,
			EntityType: row.EntityType,
			EntityID:   "-",
		}
		if row.Username != nil {
			v.Username = *row.Username
		}
		if row.EntityID != nil {
			v.EntityID = strconv.FormatInt(*row.EntityID, 10)
		}
		if row.DiffJson != nil {
			v.DiffJSON = *row.DiffJson
		}
		props.Rows = append(props.Rows, v)
	}
	if hasMore {
		// 「さらに表示」は現在のフィルタを維持し、最終表示行の id を
		// カーソルにする。
		next := url.Values{}
		if action != "" {
			next.Set("action", action)
		}
		if entityType != "" {
			next.Set("entity_type", entityType)
		}
		if username != "" {
			next.Set("username", username)
		}
		next.Set("before_id", strconv.FormatInt(rows[len(rows)-1].ID, 10))
		props.MoreURL = "/admin/audit-logs?" + next.Encode()
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := auditlogview.List(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render audit logs list", "err", err)
	}
}
