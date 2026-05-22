package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
