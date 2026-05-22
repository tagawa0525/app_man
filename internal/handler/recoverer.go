package handler

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/view/errors"
)

// recoverer は panic を補足し、レスポンスが未送信なら errors.ServerError
// テンプレで 500 を返す独自リカバラ。
//
// chi/v5/middleware の Recoverer はプレーンテキストで 500 を返す
// 仕様のため、テンプレを使えない。代わりにここで recover して
// HTML レスポンスを返す。
//
// http.ErrAbortHandler はサーバ側の意図的なアボートシグナルなので
// chi の慣習に従い再 panic させ、Go 標準の net/http に処理を委ねる。
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rvr := recover()
				if rvr == nil {
					return
				}
				if rvr == http.ErrAbortHandler {
					panic(rvr)
				}
				if logger != nil {
					logger.Error("panic recovered",
						slog.Any("recovered", rvr),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("stack", string(debug.Stack())),
					)
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				role := middleware.RoleFrom(r.Context())
				_ = errors.ServerError(role).Render(r.Context(), w)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
