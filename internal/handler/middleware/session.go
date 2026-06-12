package middleware

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/tagawa0525/app_man/internal/session"
)

// sessionKey は context への session 格納キー。
// auth.go の roleKey と同じく 0 サイズ unexported 型で衝突回避する。
type sessionKey struct{}

// SessionConfig は SessionMiddleware の依存。
//
//   - Store: 永続化境界。SQLiteStore を想定するがテストで fake も刺せる
//   - SecureCookie: Cookie に Secure を立てるか (本番 HTTPS で true)
//   - MaxAge: 新規発行時の有効期限 (= config.auth.session_max_age_hours)
//   - Now: 現在時刻取得。テストで決定論を与えるため注入。nil なら time.Now
//   - Logger: 不正 Cookie 検出・Touch エラー等の警告ログ用。nil なら slog.Default
type SessionConfig struct {
	Store        session.Store
	SecureCookie bool
	MaxAge       time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
}

// SessionMiddleware は session Cookie を読み、DB と突合して context に
// *session.Session を詰める。
//
//   - Cookie がない / Cookie 値が DB にない / DB に有るが expires_at <= now:
//     新規匿名セッションを発行し Set-Cookie で返す
//   - Cookie が有効: last_seen_at を Touch して context に詰める
//
// 認証は別 PR (LocalAuthenticator) が POST /login で session.AppUserID を
// 埋める形になる。本ミドルウェアは認証情報を一切扱わない。
func SessionMiddleware(cfg SessionConfig) func(http.Handler) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := cfg.Now()
			s, fresh := loadOrCreateSession(r.Context(), cfg, now, session.ReadCookie(r))

			if fresh {
				// 新規発行時のみ Cookie を上書きする。古い Cookie が不正だった
				// 場合も Set-Cookie で同名上書きされるので明示削除は不要。
				session.SetCookie(w, s.ID, cfg.MaxAge, cfg.SecureCookie)
			} else if err := cfg.Store.Touch(r.Context(), s.ID, now); err != nil {
				cfg.Logger.Warn("session touch failed", "err", err, "session_id", maskID(s.ID))
			}

			ctx := context.WithValue(r.Context(), sessionKey{}, s)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionFrom は context から session を取り出す。
// 未設定なら nil (ハンドラ側で「未認証なら /login へリダイレクト」等の判断を
// 行うのは AuthMiddleware の役目、本ミドルウェアは詰めるだけ)。
func SessionFrom(ctx context.Context) *session.Session {
	if v, ok := ctx.Value(sessionKey{}).(*session.Session); ok {
		return v
	}
	return nil
}

// loadOrCreateSession は Cookie 値から既存セッションを引き、無効なら新規発行する。
// 戻り値の fresh が true の場合、呼び出し側で Set-Cookie が必要。
func loadOrCreateSession(ctx context.Context, cfg SessionConfig, now time.Time, cookieID string) (*session.Session, bool) {
	if cookieID != "" {
		existing, err := cfg.Store.GetByID(ctx, cookieID)
		switch {
		case err == nil && existing.ExpiresAt.After(now):
			return existing, false
		case err == nil:
			// 期限切れ。新規発行に進む。古いレコードは DeleteExpired で掃除される
		case errors.Is(err, sql.ErrNoRows):
			// Cookie 値が DB にない。ブラウザは保持していたが server 側で消えた等
		default:
			cfg.Logger.Warn("session lookup failed", "err", err, "session_id", maskID(cookieID))
		}
	}

	created, err := createAnonymousSession(ctx, cfg.Store, now, cfg.MaxAge)
	if err != nil {
		// crypto/rand or DB INSERT 失敗。ハンドラに nil を渡せないので、
		// 永続化されない一時セッションを返してログだけ残す。
		cfg.Logger.Error("create anonymous session failed", "err", err)
		return &session.Session{
			CreatedAt:  now,
			LastSeenAt: now,
			ExpiresAt:  now,
		}, false
	}
	return created, true
}

func createAnonymousSession(ctx context.Context, store session.Store, now time.Time, maxAge time.Duration) (*session.Session, error) {
	id, err := session.NewID()
	if err != nil {
		return nil, err
	}
	tok, err := session.NewCSRFToken()
	if err != nil {
		return nil, err
	}
	s := &session.Session{
		ID:         id,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(maxAge),
	}
	if err := store.Create(ctx, *s); err != nil {
		return nil, err
	}
	return s, nil
}

// maskID はログに ID 全文を出さないための短縮形を返す (先頭 8 文字)。
// 32 byte 乱数 base64url の頭 8 文字なら逆引きできない。
func maskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
