package web

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	dashview "github.com/tagawa0525/app_man/internal/view/dashboard"
)

// dashboardHandlers は GET / (ダッシュボード最小版、Plan
// dashboard-minimal.md) を担当する。現存するデータ源で意味を持つ
// 4 ウィジェットのみを表示し、SKYSEA / AD / 棚卸し等に依存する
// ウィジェットは対応フェーズで追加する (プレースホルダは置かない)。
type dashboardHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// index は GET /。ログインフローの自然な着地点 (L-1 検証以来の 404 解消)。
//
// §5.6 のロール別出し分けはウィジェット単位のみ:
//   - (1) ライセンス保有・過不足 / (2) 承認状況サマリ: 全ロール
//   - (3) 満了間近ライセンス: general_user 以外
//   - (4) 退職者の未解除割当: license_manager 以上 (editors と同集合)
//
// 「自部署」の部署スコープ絞り込みは継続負債で、全社表示のまま。
// 非表示ウィジェットのクエリはロール判定の内側でだけ実行する
// (見せないデータを取得しない)。
func (h *dashboardHandlers) index(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())

	usage, err := q.ListLicenseUsage(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list license usage for dashboard", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	counts, err := q.CountProductsByDefaultApprovalStatus(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count products by default approval status", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	props := dashview.Props{
		Usage:          usage,
		ApprovalCounts: counts,
	}

	if role != middleware.RoleGeneralUser {
		props.ShowExpiring = true
		props.Expiring, err = q.ListExpiringLicenses(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list expiring licenses for dashboard", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if isEditorRole(role) {
		props.ShowLeavers = true
		props.Leavers, err = q.ListDeactivatedUserAssignments(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list deactivated user assignments", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashview.Index(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render dashboard", "err", err)
	}
}
