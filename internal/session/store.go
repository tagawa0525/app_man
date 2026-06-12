package session

import (
	"context"
	"database/sql"
	"time"

	"github.com/tagawa0525/app_man/internal/repository"
)

// Session は app_man の HTTP セッションを表す。
// AppUserID が nil の場合は匿名 (未ログイン) セッション。
type Session struct {
	ID         string
	AppUserID  *int64
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Store はセッションの永続化境界。SQLiteStore が唯一の実装だが、
// 後続 PR (Authenticator) でモック化したいので interface を切る。
type Store interface {
	Create(ctx context.Context, s Session) error
	GetByID(ctx context.Context, id string) (*Session, error)
	Touch(ctx context.Context, id string, now time.Time) error
	Rotate(ctx context.Context, oldID, newID string) error
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// SQLiteStore は sqlc 生成の repository.Queries を介してセッションを保存する。
type SQLiteStore struct {
	q *repository.Queries
}

// NewSQLiteStore は *sql.DB を受け取り SQLiteStore を組み立てる。
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{q: repository.New(db)}
}

// Create は新規セッションを INSERT する。
// 同 ID が既に存在する場合は UNIQUE 制約違反でエラーとなる (呼び出し側で
// ID が衝突しない前提)。
func (s *SQLiteStore) Create(ctx context.Context, sess Session) error {
	return s.q.CreateSession(ctx, repository.CreateSessionParams{
		ID:         sess.ID,
		AppUserID:  sess.AppUserID,
		CsrfToken:  sess.CSRFToken,
		CreatedAt:  sess.CreatedAt,
		LastSeenAt: sess.LastSeenAt,
		ExpiresAt:  sess.ExpiresAt,
	})
}

// GetByID は ID で 1 件取得する。存在しない場合は sql.ErrNoRows をそのまま返す。
func (s *SQLiteStore) GetByID(ctx context.Context, id string) (*Session, error) {
	row, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Session{
		ID:         row.ID,
		AppUserID:  row.AppUserID,
		CSRFToken:  row.CsrfToken,
		CreatedAt:  row.CreatedAt,
		LastSeenAt: row.LastSeenAt,
		ExpiresAt:  row.ExpiresAt,
	}, nil
}

// Touch は last_seen_at を更新する。SessionMiddleware が毎リクエスト呼ぶ。
func (s *SQLiteStore) Touch(ctx context.Context, id string, now time.Time) error {
	return s.q.TouchSession(ctx, repository.TouchSessionParams{
		LastSeenAt: now,
		ID:         id,
	})
}

// Rotate は session ID だけを差し替える。CSRF token / app_user_id /
// created_at は維持される。ログイン成功時に固定攻撃対策として呼ぶ
// (本 PR では未使用、次 PR で利用)。
func (s *SQLiteStore) Rotate(ctx context.Context, oldID, newID string) error {
	return s.q.RotateSessionID(ctx, repository.RotateSessionIDParams{
		NewID: newID,
		OldID: oldID,
	})
}

// Delete はログアウト時 (次 PR) と Cookie 不整合時に呼ぶ。
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	return s.q.DeleteSession(ctx, id)
}

// DeleteExpired は expires_at <= now のレコードを一括削除し、件数を返す。
// 起動時 GC で使う。エラーは best-effort で呼び出し側が握る。
func (s *SQLiteStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	return s.q.DeleteExpiredSessions(ctx, now)
}
