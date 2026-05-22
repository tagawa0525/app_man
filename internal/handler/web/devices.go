package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	deviceview "github.com/tagawa0525/app_man/internal/view/devices"
)

// deviceHandlers は端末系ハンドラ (List / NewForm / Create / Show /
// EditForm / Update / Retire / Restore) を束ねる。本 PR (PR-E) の
// GREEN サイクル中はメソッドを段階的に追加していく。
type deviceHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// list は GET /devices の一覧 + 検索を返す。検索は asset_code / hostname の
// 2 カラム OR LIKE。既定では現役 (retired_at IS NULL) のみ。
// ?include_inactive=1 で退役端末も含める。検索 ?q= と組合せ可能。
func (h *deviceHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	includeInactive := r.URL.Query().Get("include_inactive") == "1"

	var (
		devices []repository.Device
		err     error
	)
	switch {
	case query != "" && includeInactive:
		devices, err = q.SearchDevicesIncludingInactive(r.Context(), likePattern(query))
	case query != "":
		devices, err = q.SearchDevices(r.Context(), likePattern(query))
	case includeInactive:
		devices, err = q.ListDevicesIncludingInactive(r.Context())
	default:
		devices, err = q.ListDevices(r.Context())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list devices", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	items, err := buildDeviceListItems(r, q, devices)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve users/departments for devices list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	truncated := len(devices) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := deviceview.List(role, query, includeInactive, items, truncated).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render devices list", "err", err)
	}
}

// newForm は GET /devices/new の新規作成フォームを返す。
// primary_user_id / department_id の select 用に現役レコードを取得する。
func (h *deviceHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	users, err := q.ListActiveUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active users for new device form", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for new device form", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, http.StatusOK, deviceview.FormProps{
		Action:      "/devices",
		Title:       "端末新規作成",
		Submit:      "作成",
		Users:       users,
		Departments: depts,
	})
}

func (h *deviceHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props deviceview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	role := middleware.RoleFrom(r.Context())
	if err := deviceview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render devices form", "err", err)
	}
}

// create は POST /devices の新規作成。検証エラー時は 400/409 で
// 同じフォームを再描画。成功時は 303 で /devices/:id へ。
func (h *deviceHandlers) create(w http.ResponseWriter, r *http.Request) {
	in, parsed, errs := decodeDeviceForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		h.renderCreateError(w, r, http.StatusBadRequest, in, errs)
		return
	}

	d, err := q.CreateDevice(r.Context(), repository.CreateDeviceParams{
		AssetCode:     parsed.AssetCode,
		Hostname:      parsed.Hostname,
		PrimaryUserID: parsed.PrimaryUserID,
		DepartmentID:  parsed.DepartmentID,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			h.renderCreateError(w, r, http.StatusConflict, in, map[string]string{
				"asset_code": "資産コードが重複しています",
			})
			return
		}
		if isForeignKeyErr(err) {
			h.renderCreateError(w, r, http.StatusBadRequest, in, fkErrorFields(parsed))
			return
		}
		h.logger.ErrorContext(r.Context(), "create device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/devices/"+strconv.FormatInt(d.ID, 10), http.StatusSeeOther)
}

// renderCreateError は create / update 失敗時に再描画する。selects は
// 都度 fetch する (この経路はホットパスではない)。
func (h *deviceHandlers) renderCreateError(w http.ResponseWriter, r *http.Request, status int, in deviceview.FormInput, errs map[string]string) {
	q := repository.New(h.db)
	users, err := q.ListActiveUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users on create error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list departments on create error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, status, deviceview.FormProps{
		Action:      "/devices",
		Title:       "端末新規作成",
		Submit:      "作成",
		Input:       in,
		Errors:      errs,
		Users:       users,
		Departments: depts,
	})
}

// show は GET /devices/:id の詳細を返す。
func (h *deviceHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	d, err := q.GetDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, perr := lookupUser(r, q, d.PrimaryUserID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup user for device", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dept, perr := lookupDepartmentForDevice(r, q, d.DepartmentID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup department for device", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := deviceview.Show(role, deviceview.ShowProps{Device: d, User: user, Department: dept}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render device show", "err", err)
	}
}

// editForm は GET /devices/:id/edit の編集フォームを返す。
func (h *deviceHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	d, err := q.GetDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get device for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	users, err := q.ListActiveUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active users for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinnedUser, err := resolvePinnedUser(r, q, users, d.PrimaryUserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve pinned user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinnedDept, err := resolvePinnedDepartment(r, q, depts, d.DepartmentID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve pinned department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, deviceview.FormProps{
		Action:           "/devices/" + strconv.FormatInt(d.ID, 10),
		Title:            "端末編集",
		Submit:           "更新",
		Input:            formInputFromDevice(d),
		Users:            users,
		Departments:      depts,
		PinnedUser:       pinnedUser,
		PinnedDepartment: pinnedDept,
	})
}

// update は POST /devices/:id の更新。
func (h *deviceHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	in, parsed, errs := decodeDeviceForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		h.renderUpdateError(w, r, id, http.StatusBadRequest, in, parsed, errs)
		return
	}

	if _, err := q.UpdateDevice(r.Context(), repository.UpdateDeviceParams{
		AssetCode:     parsed.AssetCode,
		Hostname:      parsed.Hostname,
		PrimaryUserID: parsed.PrimaryUserID,
		DepartmentID:  parsed.DepartmentID,
		ID:            id,
	}); err != nil {
		if isUniqueConstraintErr(err) {
			h.renderUpdateError(w, r, id, http.StatusConflict, in, parsed, map[string]string{
				"asset_code": "資産コードが重複しています",
			})
			return
		}
		if isForeignKeyErr(err) {
			h.renderUpdateError(w, r, id, http.StatusBadRequest, in, parsed, fkErrorFields(parsed))
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/devices/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// renderUpdateError は update 失敗時に編集フォームを再描画する。
// pinned option も解決して退職 user / 廃止部署を残す。
func (h *deviceHandlers) renderUpdateError(w http.ResponseWriter, r *http.Request, id int64, status int, in deviceview.FormInput, parsed deviceParsed, errs map[string]string) {
	q := repository.New(h.db)
	users, err := q.ListActiveUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users on update error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list departments on update error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinnedUser, err := resolvePinnedUser(r, q, users, parsed.PrimaryUserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinnedDept, err := resolvePinnedDepartment(r, q, depts, parsed.DepartmentID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, status, deviceview.FormProps{
		Action:           "/devices/" + strconv.FormatInt(id, 10),
		Title:            "端末編集",
		Submit:           "更新",
		Input:            in,
		Errors:           errs,
		Users:            users,
		Departments:      depts,
		PinnedUser:       pinnedUser,
		PinnedDepartment: pinnedDept,
	})
}

// retire は POST /devices/:id/retire の論理削除 (retired_at を立てる)。
// 既に退役済みなら 409 + flash 付きで show を再描画する。
func (h *deviceHandlers) retire(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.SoftDeleteDevice(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "soft delete device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		if _, gerr := q.GetDevice(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "この端末は既に退役済みです。")
		return
	}
	http.Redirect(w, r, "/devices/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// restore は POST /devices/:id/restore の論理削除取り消し
// (retired_at を NULL に戻す)。既に稼働中なら 409 + flash 付き再描画。
func (h *deviceHandlers) restore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.RestoreDevice(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "restore device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		if _, gerr := q.GetDevice(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "この端末は既に稼働中です。")
		return
	}
	http.Redirect(w, r, "/devices/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// showWithFlash は status を付けて show templ を再描画する (409 用)。
// エラーハンドリングは show ハンドラと同じレベルに揃える。
func (h *deviceHandlers) showWithFlash(w http.ResponseWriter, r *http.Request, id int64, status int, flash string) {
	q := repository.New(h.db)
	d, err := q.GetDevice(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get device for flash", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	user, perr := lookupUser(r, q, d.PrimaryUserID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup user for flash", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dept, perr := lookupDepartmentForDevice(r, q, d.DepartmentID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup department for flash", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := deviceview.Show(role, deviceview.ShowProps{Device: d, User: user, Department: dept, Flash: flash}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render device show on conflict", "err", err)
	}
}

// fkErrorFields は parsed の FK 値から、どちらの FK が未存在かを推定して
// エラーマップを返す。両方指定されていた場合は両方にメッセージを乗せる。
// (実環境では片方が NULL のケースが多く、メッセージ重複は気にしない)
func fkErrorFields(parsed deviceParsed) map[string]string {
	errs := map[string]string{}
	if parsed.PrimaryUserID != nil {
		errs["primary_user_id"] = "指定されたユーザは存在しません"
	}
	if parsed.DepartmentID != nil {
		errs["department_id"] = "指定された部署は存在しません"
	}
	return errs
}

// deviceParsed は decodeDeviceForm がパースした sqlc 用パラメータ。
type deviceParsed struct {
	AssetCode     string
	Hostname      *string
	PrimaryUserID *int64
	DepartmentID  *int64
}

// decodeDeviceForm は POST フォームから入力を取り出し、必須項目と
// 形式を検証する。戻り値は (生入力, 解析済み, エラーマップ)。
func decodeDeviceForm(r *http.Request) (deviceview.FormInput, deviceParsed, map[string]string) {
	_ = r.ParseForm()
	in := deviceview.FormInput{
		AssetCode:     strings.TrimSpace(r.PostFormValue("asset_code")),
		Hostname:      strings.TrimSpace(r.PostFormValue("hostname")),
		PrimaryUserID: strings.TrimSpace(r.PostFormValue("primary_user_id")),
		DepartmentID:  strings.TrimSpace(r.PostFormValue("department_id")),
	}
	errs := map[string]string{}
	if msg := validateAsciiCode("資産コード", 64, in.AssetCode); msg != "" {
		errs["asset_code"] = msg
	}
	if msg := validateHostname(in.Hostname); msg != "" {
		errs["hostname"] = msg
	}
	userID, uerr := parseInt64Opt(in.PrimaryUserID)
	if uerr != "" {
		errs["primary_user_id"] = uerr
	}
	deptID, derr := parseInt64Opt(in.DepartmentID)
	if derr != "" {
		errs["department_id"] = derr
	}
	return in, deviceParsed{
		AssetCode:     in.AssetCode,
		Hostname:      nilIfEmpty(in.Hostname),
		PrimaryUserID: userID,
		DepartmentID:  deptID,
	}, errs
}

// formInputFromDevice は既存レコードを編集フォーム入力値に詰め直す。
func formInputFromDevice(d repository.Device) deviceview.FormInput {
	out := deviceview.FormInput{
		AssetCode: d.AssetCode,
	}
	if d.Hostname != nil {
		out.Hostname = *d.Hostname
	}
	if d.PrimaryUserID != nil {
		out.PrimaryUserID = strconv.FormatInt(*d.PrimaryUserID, 10)
	}
	if d.DepartmentID != nil {
		out.DepartmentID = strconv.FormatInt(*d.DepartmentID, 10)
	}
	return out
}

// resolvePinnedUser は users (現役) に含まれていない参照先 user を 1 件
// fetch する。編集中レコードが退職済 user を指す場合に option を残すため。
// id が nil もしくは既に users に含まれる場合は nil。
// sql.ErrNoRows は nil 扱い。
func resolvePinnedUser(r *http.Request, q *repository.Queries, users []repository.User, id *int64) (*repository.User, error) {
	if id == nil {
		return nil, nil
	}
	for _, u := range users {
		if u.ID == *id {
			return nil, nil
		}
	}
	u, err := q.GetUser(r.Context(), *id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// lookupUser は *int64 が nil なら nil を返し、そうでなければ id で fetch する。
// sql.ErrNoRows は nil 扱い (整合性が崩れていても show 画面は描画したい)。
func lookupUser(r *http.Request, q *repository.Queries, id *int64) (*repository.User, error) {
	if id == nil {
		return nil, nil
	}
	u, err := q.GetUser(r.Context(), *id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// lookupDepartmentForDevice は users 側の lookupDepartmentForUser と同型。
// 3 度目だが lookup は単純なので個別保持 (共通化対象外)。
func lookupDepartmentForDevice(r *http.Request, q *repository.Queries, id *int64) (*repository.Department, error) {
	if id == nil {
		return nil, nil
	}
	d, err := q.GetDepartment(r.Context(), *id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// validateHostname は hostname フィールドの検証 (任意、255 文字以内)。
// Windows NetBIOS の 15 文字制約は厳しすぎ、FQDN まで含む実利用形態を許容する。
func validateHostname(s string) string {
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) > 255 {
		return "ホスト名は 255 文字以内で入力してください"
	}
	return ""
}

// buildDeviceListItems は devices スライスに対応する主利用者と所属部署を
// 解決して list templ が要求する ListItem に詰め替える。同じ ID は再 fetch
// を避けるためキャッシュする。
func buildDeviceListItems(r *http.Request, q *repository.Queries, devices []repository.Device) ([]deviceview.ListItem, error) {
	userCache := make(map[int64]*repository.User)
	deptCache := make(map[int64]*repository.Department)
	out := make([]deviceview.ListItem, 0, len(devices))
	for _, d := range devices {
		var user *repository.User
		if d.PrimaryUserID != nil {
			id := *d.PrimaryUserID
			if v, ok := userCache[id]; ok {
				user = v
			} else {
				u, err := q.GetUser(r.Context(), id)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						userCache[id] = nil
					} else {
						return nil, err
					}
				} else {
					user = &u
					userCache[id] = user
				}
			}
		}
		var dept *repository.Department
		if d.DepartmentID != nil {
			id := *d.DepartmentID
			if v, ok := deptCache[id]; ok {
				dept = v
			} else {
				dd, err := q.GetDepartment(r.Context(), id)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						deptCache[id] = nil
					} else {
						return nil, err
					}
				} else {
					dept = &dd
					deptCache[id] = dept
				}
			}
		}
		out = append(out, deviceview.ListItem{Device: d, User: user, Department: dept})
	}
	return out, nil
}
