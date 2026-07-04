package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// assignments.go はライセンス詳細画面の割当セクションを駆動するハンドラ
// (フェーズ 6 L-2)。割当専用画面は仕様 §6.1 に存在しないため、追加・解除の
// POST 後は必ずライセンス詳細へ 303 で戻す。
//
// - 追加: 対象の実在チェック (在職ユーザ / 現役端末のみ) → アクティブ重複
//   チェック (409) → INSERT。事前チェック後のレースは部分 UNIQUE インデックス
//   uniq_user_assignments_active / uniq_device_assignments_active
//   (migration 000006) の違反として現れるので 409 に変換する
// - 解除: revoked_at を埋める論理解除 (:execrows)。0 行なら 404
//   (既に解除済み / 他ライセンスの割当 ID)。二重 POST に安全
// - エラー表示: products のエイリアス重複と同じ流儀で、詳細画面を flash
//   付きで再描画する (専用フォーム画面がないため)

// assignUser は POST /licenses/{id}/assignments/users。
func (h *licenseHandlers) assignUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	if !h.licenseExists(w, r, q, id) {
		return
	}

	_ = r.ParseForm()
	rawUID := strings.TrimSpace(r.PostFormValue("user_id"))
	uid, err := strconv.ParseInt(rawUID, 10, 64)
	if rawUID == "" || err != nil {
		h.renderShow(w, r, id, http.StatusBadRequest, "割当するユーザを選択してください。")
		return
	}

	u, err := q.GetUser(r.Context(), uid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderShow(w, r, id, http.StatusBadRequest, "指定されたユーザが存在しません。")
			return
		}
		h.logger.ErrorContext(r.Context(), "get user for assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u.DeactivatedAt != nil {
		h.renderShow(w, r, id, http.StatusBadRequest, "退職済みのユーザには割当できません。")
		return
	}

	cnt, err := q.CountActiveUserAssignment(r.Context(), repository.CountActiveUserAssignmentParams{
		LicenseID: id,
		UserID:    uid,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count active user assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if cnt > 0 {
		h.renderShow(w, r, id, http.StatusConflict, "このユーザには既に割当済みです。")
		return
	}

	if _, err := q.CreateUserAssignment(r.Context(), repository.CreateUserAssignmentParams{
		LicenseID:         id,
		UserID:            uid,
		ExternalAccountID: nilIfEmpty(strings.TrimSpace(r.PostFormValue("external_account_id"))),
		Note:              nilIfEmpty(strings.TrimSpace(r.PostFormValue("note"))),
	}); err != nil {
		if isUniqueConstraintErr(err) {
			// 事前チェック後のレース (並行 POST) は uniq_user_assignments_active
			// (000006) の UNIQUE 違反で現れる。
			h.renderShow(w, r, id, http.StatusConflict, "このユーザには既に割当済みです。")
			return
		}
		h.logger.ErrorContext(r.Context(), "create user assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// revokeUserAssignment は POST /licenses/{id}/assignments/users/{aid}/revoke。
func (h *licenseHandlers) revokeUserAssignment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	aid, ok := parseInt64Param(r, "aid")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.RevokeUserAssignment(r.Context(), repository.RevokeUserAssignmentParams{
		ID:        aid,
		LicenseID: id,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke user assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// assignDevice は POST /licenses/{id}/assignments/devices。
func (h *licenseHandlers) assignDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	if !h.licenseExists(w, r, q, id) {
		return
	}

	_ = r.ParseForm()
	rawDID := strings.TrimSpace(r.PostFormValue("device_id"))
	did, err := strconv.ParseInt(rawDID, 10, 64)
	if rawDID == "" || err != nil {
		h.renderShow(w, r, id, http.StatusBadRequest, "割当する端末を選択してください。")
		return
	}

	d, err := q.GetDevice(r.Context(), did)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderShow(w, r, id, http.StatusBadRequest, "指定された端末が存在しません。")
			return
		}
		h.logger.ErrorContext(r.Context(), "get device for assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if d.RetiredAt != nil {
		h.renderShow(w, r, id, http.StatusBadRequest, "退役済みの端末には割当できません。")
		return
	}

	cnt, err := q.CountActiveDeviceAssignment(r.Context(), repository.CountActiveDeviceAssignmentParams{
		LicenseID: id,
		DeviceID:  did,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count active device assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if cnt > 0 {
		h.renderShow(w, r, id, http.StatusConflict, "この端末には既に割当済みです。")
		return
	}

	if _, err := q.CreateDeviceAssignment(r.Context(), repository.CreateDeviceAssignmentParams{
		LicenseID: id,
		DeviceID:  did,
		Note:      nilIfEmpty(strings.TrimSpace(r.PostFormValue("note"))),
	}); err != nil {
		if isUniqueConstraintErr(err) {
			// 事前チェック後のレース (並行 POST) は uniq_device_assignments_active
			// (000006) の UNIQUE 違反で現れる。
			h.renderShow(w, r, id, http.StatusConflict, "この端末には既に割当済みです。")
			return
		}
		h.logger.ErrorContext(r.Context(), "create device assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// revokeDeviceAssignment は POST /licenses/{id}/assignments/devices/{aid}/revoke。
func (h *licenseHandlers) revokeDeviceAssignment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	aid, ok := parseInt64Param(r, "aid")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.RevokeDeviceAssignment(r.Context(), repository.RevokeDeviceAssignmentParams{
		ID:        aid,
		LicenseID: id,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke device assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// licenseExists は割当 POST の対象ライセンスの実在確認。存在しなければ
// 404 を書き込んで false を返す (未知 license_id への INSERT が FK 違反 →
// 500 になるのを防ぐ)。
func (h *licenseHandlers) licenseExists(w http.ResponseWriter, r *http.Request, q *repository.Queries, id int64) bool {
	if _, err := q.GetLicenseByID(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return false
		}
		h.logger.ErrorContext(r.Context(), "get license for assignment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	return true
}
