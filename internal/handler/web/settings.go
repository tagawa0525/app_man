package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	settingview "github.com/tagawa0525/app_man/internal/view/settings"
)

// settings.go は設定値管理画面 (仕様 §5.11 / Plan admin-settings.md) の web 層:
//
//   - GET  /admin/settings              既知キーの一覧 (既定値フォールバック表示)
//   - POST /admin/settings/{key}        更新 (TrimSpace → 正整数検証 → UPSERT)
//   - POST /admin/settings/{key}/reset  既定値へ戻す (行 DELETE)
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。更新・リセットは
// audit_logs (app_setting.change / app_setting.reset) と同一トランザクション
// で記録し、記録なしの変更を作らない (承認系と同方針)。
type settingHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// appSettingDef は編集可能な既知キーのレジストリ 1 行。
type appSettingDef struct {
	Key         string
	Description string
	// Consumer はこのキーを読むバイナリ名 (運用者が影響範囲を画面で
	// 判断できるように表示する)。
	Consumer string
	// Note は補足 (未実装の消費者等)。空なら表示しない。
	Note string
	// Default はキーの行が無いときに Consumer 側が使う既定値。
	Default int
}

// knownAppSettings は編集対象キーの固定リスト。キー・既定値の正本は
// 仕様 §5.11 の表 (cmd/prune-logs/runner.go の default* 定数も同じ表に
// 一致させている)。任意キーの新規作成は許さない: 自由記入はタイポで
// 「効かない設定」を作る事故源のため、消費者が増えたらここに足す (Plan)。
// 5 キーとも値は正整数のみ (resolveRetentionDays と同じ検証基準)。
var knownAppSettings = []appSettingDef{
	{
		Key:         "notification_max_retry",
		Description: "通知再送の上限回数",
		Consumer:    "appmgr-notify",
		Note:        "appmgr-notify は未実装 (通知フェーズで導入予定)",
		Default:     5,
	},
	{
		Key:         "retention_days_audit_logs",
		Description: "audit_logs の保持期間 (日)",
		Consumer:    "appmgr-prune-logs",
		Default:     1825,
	},
	{
		Key:         "retention_days_raw_installations",
		Description: "raw_installations の保持期間 (日)",
		Consumer:    "appmgr-prune-logs",
		Default:     365,
	},
	{
		Key:         "retention_days_import_logs",
		Description: "import_logs の保持期間 (日)",
		Consumer:    "appmgr-prune-logs",
		Default:     1095,
	},
	{
		Key:         "retention_days_notifications_sent",
		Description: "notifications (送信済み) の保持期間 (日)",
		Consumer:    "appmgr-prune-logs",
		Default:     365,
	},
}

// findAppSettingDef は knownAppSettings から key の定義を探す。
func findAppSettingDef(key string) (appSettingDef, bool) {
	for _, def := range knownAppSettings {
		if def.Key == key {
			return def, true
		}
	}
	return appSettingDef{}, false
}

// appSettingChangeDiff は app_setting.change の diff_json。app_settings は
// key が PK で数値 id を持たないため entity_id は NULL、対象キーはここで
// 持つ。未設定からの変更は old を省略する。
type appSettingChangeDiff struct {
	Key string `json:"key"`
	Old string `json:"old,omitempty"`
	New string `json:"new"`
}

// appSettingResetDiff は app_setting.reset の diff_json。削除した行の値を
// old として残す (NULL 値の行だった場合は省略)。
type appSettingResetDiff struct {
	Key string `json:"key"`
	Old string `json:"old,omitempty"`
}

// renderList は一覧を描画する。既知キーは knownAppSettings の順、DB にしか
// ない未知キーは key 昇順 (ListAppSettings の ORDER BY) で表末尾に読み取り
// 専用表示する。
func (h *settingHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, flash string) {
	q := repository.New(h.db)
	stored, err := q.ListAppSettings(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list app settings", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byKey := make(map[string]repository.AppSetting, len(stored))
	for _, s := range stored {
		byKey[s.Key] = s
	}

	props := settingview.ListProps{Flash: flash}
	for _, def := range knownAppSettings {
		row := settingview.Row{
			Key:         def.Key,
			Description: def.Description,
			Consumer:    def.Consumer,
			Note:        def.Note,
			Default:     def.Default,
		}
		if s, ok := byKey[def.Key]; ok {
			row.IsSet = true
			if s.Value != nil {
				row.Value = *s.Value
			}
		}
		props.Rows = append(props.Rows, row)
	}
	for _, s := range stored {
		if _, known := findAppSettingDef(s.Key); known {
			continue
		}
		u := settingview.UnknownRow{Key: s.Key}
		if s.Value != nil {
			u.Value = *s.Value
		}
		props.Unknown = append(props.Unknown, u)
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := settingview.List(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render settings list", "err", err)
	}
}

// list は GET /admin/settings。
func (h *settingHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, "")
}

// update は POST /admin/settings/{key}。既知キーのみ (固定リスト外は 404)。
// 値は TrimSpace してから正整数検証する: prune-logs の resolveRetentionDays
// は trim しない厳格検証のため、空白起因の exit 1 を入口で防ぐ (Plan)。
// 保存値は strconv.Itoa で正規化する (" 30 " → "30")。
func (h *settingHandlers) update(w http.ResponseWriter, r *http.Request) {
	def, ok := findAppSettingDef(chi.URLParam(r, "key"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	raw := strings.TrimSpace(r.PostFormValue("value"))
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		h.renderList(w, r, http.StatusBadRequest,
			def.Key+" の値が不正です。1 以上の整数を入力してください。")
		return
	}
	value := strconv.Itoa(n)

	var updatedBy *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		updatedBy = sess.AppUserID
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for app setting change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Commit 成功後の Rollback は no-op (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	// diff_json の old を取るため、同一 tx 内で変更前の値を読む。
	diff := appSettingChangeDiff{Key: def.Key, New: value}
	old, err := qtx.GetAppSetting(r.Context(), def.Key)
	switch {
	case err == nil:
		if old.Value != nil {
			diff.Old = *old.Value
		}
	case errors.Is(err, sql.ErrNoRows):
		// 未設定からの変更 → old は省略。
	default:
		h.logger.ErrorContext(r.Context(), "get app setting before change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := qtx.UpsertAppSetting(r.Context(), repository.UpsertAppSettingParams{
		Key:                def.Key,
		Value:              &value,
		UpdatedByAppUserID: updatedBy,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "upsert app setting", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 記録なしの変更を作らない: audit INSERT 失敗時は変更ごとロールバック。
	if err := recordAuditDiffEntity(r.Context(), qtx, r, "app_setting.change", "app_setting", nil, diff); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for app setting change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit app setting change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

// reset は POST /admin/settings/{key}/reset。行 DELETE でキー不在 = 既定値の
// 状態に戻す (Plan: 「既定値と同じ値の行」を残すより状態が明確)。行が無い
// キー・未知キーは 404。
func (h *settingHandlers) reset(w http.ResponseWriter, r *http.Request) {
	def, ok := findAppSettingDef(chi.URLParam(r, "key"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for app setting reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	old, err := qtx.GetAppSetting(r.Context(), def.Key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app setting before reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	affected, err := qtx.DeleteAppSetting(r.Context(), def.Key)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete app setting", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		// GetAppSetting 通過後に並行リセットされたレース。
		_ = tx.Rollback()
		http.NotFound(w, r)
		return
	}
	diff := appSettingResetDiff{Key: def.Key}
	if old.Value != nil {
		diff.Old = *old.Value
	}
	if err := recordAuditDiffEntity(r.Context(), qtx, r, "app_setting.reset", "app_setting", nil, diff); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for app setting reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit app setting reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}
