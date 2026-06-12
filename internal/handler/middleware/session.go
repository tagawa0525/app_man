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

	// ID / CSRF token を先に生成してから永続化を試みる。Create が失敗しても
	// このリクエストで使える ID / CSRF token は手元にあるので、Set-Cookie で
	// クライアントに返し、ハンドラには有効な session を渡す (fresh=true)。
	// 次リクエストの Cookie 値は DB に無いため、また loadOrCreateSession で
	// 新規発行され、通常フローに合流する。
	id, err := session.NewID()
	if err != nil {
		cfg.Logger.Error("generate session ID failed", "err", err)
		return ephemeralSession(now), false
	}
	tok, err := session.NewCSRFToken()
	if err != nil {
		cfg.Logger.Error("generate CSRF token failed", "err", err)
		return ephemeralSession(now), false
	}
	s := &session.Session{
		ID:         id,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(cfg.MaxAge),
	}
	if err := cfg.Store.Create(ctx, *s); err != nil {
		// 永続化失敗。ID / CSRF token はこのリクエスト内で有効なので fresh=true
		// で返し、Cookie 発行 + ハンドラ続行は行う。リクエスト跨ぎで session が
		// 続かない点はクライアントから見ると「次のリクエストでログイン情報が
		// 切れた」と同じ挙動になる。
		cfg.Logger.Error("persist anonymous session failed (using ephemeral session for this request)", "err", err, "session_id", maskID(s.ID))
	}
	return s, true
}

// ephemeralSession は ID / CSRF 生成すらできなかった極端なケースで返す。
// 下流ハンドラが CSRFToken を使うと空文字検証になり 403 で弾かれるが、
// 実害は「このリクエストが 403 になる」のみで、サーバプロセスは生き残る。
func ephemeralSession(now time.Time) *session.Session {
	return &session.Session{
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now,
	}
}

// maskID はログに ID 全文を出さないための短縮形を返す (先頭 8 文字)。
// 32 byte 乱数 base64url の頭 8 文字なら逆引きできない。
func maskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
