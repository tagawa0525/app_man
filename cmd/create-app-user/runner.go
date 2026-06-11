package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/lockfile"
	"github.com/tagawa0525/app_man/internal/repository"
)

// envInitialPassword は readPassword が優先的に読む環境変数名。
// CI / docker などスクリプト経由で再現可能な投入経路として用意する。
const envInitialPassword = "APPMGR_INITIAL_PASSWORD"

// runOptions は CLI flag を解釈した後の正規化済み引数。
type runOptions struct {
	configPath     string
	username       string
	role           string // create モードでのみ意味を持つ
	departmentCode string // create モード + system_admin 以外で必須
	notifyEmail    string // create モードでのみ意味を持つ。空欄可
	resetPassword  bool   // true なら reset モード、false なら create モード
}

// run は本体。テストから差し替え可能になるよう stdin / stdout / stderr / getenv を
// 引数で受ける。clirun.Run は flag 受け取り口を持たないため独立実装にする
// (import-bootstrap と同方針)。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := runOptions{}
	fs.StringVar(&opts.configPath, "config", "config.yml", "path to config.yml")
	fs.StringVar(&opts.username, "username", "", "username (required for both modes)")
	fs.StringVar(&opts.role, "role", "system_admin", "role to grant (create mode only)")
	fs.StringVar(&opts.departmentCode, "department-code", "", "department code (required for non-system_admin roles)")
	fs.StringVar(&opts.notifyEmail, "notify-email", "", "notification email (recommended for local admins)")
	fs.BoolVar(&opts.resetPassword, "reset-password", false, "reset password for an existing user")
	if err := fs.Parse(args); err != nil {
		return exitConfigInvalid
	}

	if err := validateFlags(opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", binaryName, err)
		return exitConfigInvalid
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: load config: %v\n", binaryName, err)
		return exitConfigInvalid
	}

	logger, closeLog, err := applog.New(cfg.Logging, binaryName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: init logger: %v\n", binaryName, err)
		return exitConfigInvalid
	}
	defer func() {
		if cerr := closeLog(); cerr != nil {
			_, _ = fmt.Fprintf(stderr, "%s: close log: %v\n", binaryName, cerr)
		}
	}()

	lock, err := lockfile.Acquire(cfg.Locks.BaseDir, binaryName, lockfile.ModeShared)
	if err != nil {
		if errors.Is(err, lockfile.ErrAlreadyHeld) {
			logger.Warn("lock already held by another process; exiting", slog.String("error", err.Error()))
			return exitLockConflict
		}
		logger.Error("acquire lock", slog.Any("error", err))
		return exitHandlerError
	}
	defer func() {
		if rerr := lock.Release(); rerr != nil {
			logger.Error("release lock", slog.Any("error", rerr))
		}
	}()

	sqlDB, closeDB, err := db.Open(cfg.Database)
	if err != nil {
		logger.Error("open db", slog.Any("error", err))
		return exitHandlerError
	}
	defer func() {
		if cerr := closeDB(); cerr != nil {
			logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mode := "create"
	if opts.resetPassword {
		mode = "reset"
	}
	logger.Info("create-app-user starting",
		slog.String("mode", mode),
		slog.String("username", opts.username),
		slog.String("role", opts.role),
		slog.String("department_code", opts.departmentCode),
	)

	q := repository.New(sqlDB)
	existing, err := q.GetAppUserByUsername(ctx, opts.username)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if opts.resetPassword {
			_, _ = fmt.Fprintf(stderr, "%s: user %q does not exist\n", binaryName, opts.username)
			return exitHandlerError
		}
		// create モードはこれが正常 (新規作成)
	case err == nil:
		if !opts.resetPassword {
			_, _ = fmt.Fprintf(stderr, "%s: user %q already exists\n", binaryName, opts.username)
			return exitHandlerError
		}
		_ = existing // auth_type 検証は resetPassword 関数内で実施
	default:
		logger.Error("lookup app_user", slog.Any("error", err))
		return exitHandlerError
	}

	// create モード: 部署解決を password 入力より前に実行する。flag ミス
	// (存在しない部署 / 廃止済み部署) は config エラー (exit 3) として
	// 扱い、ユーザに無駄なパスワード入力をさせない。INSERT 中の DB エラー
	// は handler error (exit 1)。
	var departmentID *int64
	if !opts.resetPassword {
		departmentID, err = resolveDepartmentID(ctx, q, opts.role, opts.departmentCode)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", binaryName, err)
			return exitConfigInvalid
		}
	}

	plaintext, err := readPassword(stdin, stdout, getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", binaryName, err)
		return exitConfigInvalid
	}
	passwordHash, err := auth.Hash(plaintext)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: hash password: %v\n", binaryName, err)
		return exitHandlerError
	}

	if opts.resetPassword {
		if err := resetPassword(ctx, sqlDB, opts.username, passwordHash); err != nil {
			logger.Error("reset password", slog.Any("error", err))
			return exitHandlerError
		}
		// audit hook 挿入点 (audit_logs PR で追加): app_user.password_reset
		_, _ = fmt.Fprintf(stdout, "reset password for username=%s\n", opts.username)
		logger.Info("password reset done", slog.String("username", opts.username))
		return exitOK
	}

	if err := createUser(ctx, sqlDB, opts, passwordHash, departmentID); err != nil {
		logger.Error("create user", slog.Any("error", err))
		return exitHandlerError
	}
	// audit hook 挿入点 (audit_logs PR で追加): app_user.create
	created, err := q.GetAppUserByUsername(ctx, opts.username)
	if err != nil {
		// 直前に commit したので通常起こらないが、念のため
		logger.Warn("re-fetch created user", slog.Any("error", err))
		_, _ = fmt.Fprintf(stdout, "created app_user username=%s role=%s\n", opts.username, opts.role)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "created app_user id=%d username=%s role=%s\n", created.ID, created.Username, opts.role)
	logger.Info("create done",
		slog.Int64("id", created.ID),
		slog.String("username", created.Username),
		slog.String("role", opts.role),
	)
	return exitOK
}

// validateFlags は flag.Parse 後の opts に対する必須/排他チェック。
// create / reset 両モードで username は必須。create モードでは role が
// 有効値であること、system_admin 以外なら department-code が必須。
func validateFlags(opts runOptions) error {
	if opts.username == "" {
		return errors.New("--username is required")
	}
	if opts.resetPassword {
		return nil // reset モードでは role / department-code は不要
	}
	if opts.role == "" {
		return errors.New("--role is required (create mode)")
	}
	if !middleware.IsValidRole(middleware.Role(opts.role)) {
		return fmt.Errorf("invalid --role: %q (allowed: %s)", opts.role, joinRoles(middleware.AllRoles()))
	}
	if opts.role != "system_admin" && opts.departmentCode == "" {
		return fmt.Errorf("--department-code is required for role %q", opts.role)
	}
	return nil
}

// createUser は app_users INSERT と user_department_roles INSERT を
// 1 トランザクションで実行する。部分挿入 (app_users だけ残ってロールが
// 付与されない状態) を防ぐため、tx 内で 2 件続けて書き込み、どちらかが
// 失敗したら全件ロールバックする。
//
// departmentID は呼び出し側で resolveDepartmentID 済みの値を渡す。
// system_admin (全社) なら nil、それ以外なら DB 解決済みの id。
// 本関数は flag 検証・パスワード入力・lock 取得・部署解決はしない。
// 戻り値の error は app_users / user_department_roles INSERT のどちらか
// に起因するエラーを wrap して返す。呼び出し側は exit code を決める。
func createUser(ctx context.Context, sqlDB *sql.DB, opts runOptions, passwordHash string, departmentID *int64) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback は Commit 成功時には no-op になる (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()

	q := repository.New(sqlDB).WithTx(tx)

	hashPtr := &passwordHash
	notifyPtr := nullableString(opts.notifyEmail)

	user, err := q.CreateAppUser(ctx, repository.CreateAppUserParams{
		Username:     opts.username,
		PasswordHash: hashPtr,
		LinkedUserID: nil, // ローカル admin は linked_user_id NULL
		NotifyEmail:  notifyPtr,
		AuthType:     "local",
	})
	if err != nil {
		return fmt.Errorf("create app_user: %w", err)
	}

	if _, err := q.CreateUserDepartmentRole(ctx, repository.CreateUserDepartmentRoleParams{
		AppUserID:    user.ID,
		DepartmentID: departmentID,
		Role:         opts.role,
	}); err != nil {
		return fmt.Errorf("create user_department_role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// resolveDepartmentID は role と department-code から department_id を引く。
// system_admin は全社スコープのため code は無視し (nil, nil) を返す。
// それ以外の role で code が空ならエラー。code 指定があれば DB から引く。
//
// 廃止済み部署 (valid_to IS NOT NULL) は拒否する。運用上、廃止部署への
// ロール付与は事故である可能性が高い。
func resolveDepartmentID(ctx context.Context, q *repository.Queries, role, code string) (*int64, error) {
	if role == "system_admin" {
		return nil, nil
	}
	if code == "" {
		return nil, fmt.Errorf("--department-code is required for role %q", role)
	}
	dept, err := q.GetDepartmentByCode(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("department not found: %s", code)
		}
		return nil, fmt.Errorf("lookup department %q: %w", code, err)
	}
	if dept.ValidTo != nil {
		return nil, fmt.Errorf("department %s is retired (valid_to=%s)", code, dept.ValidTo.Format("2006-01-02"))
	}
	return &dept.ID, nil
}

// resetPassword は指定 username の password_hash を上書きする。
//
// 仕様書 §7.3 / §4.2 のとおり、ローカル認証 (auth_type='local') ユーザ
// 専用。AD パススルー認証 (auth_type='ad') のユーザはパスワードを AD で
// 管理しており、こちら側で password_hash を持つことは無いため、
// auth_type が 'local' 以外なら拒否する (誤って AD ユーザの app_users 行に
// ハッシュを書き込むのを防ぐ防御層)。
//
// roles や notify_email など他フィールドは触らない。reset 専用。
func resetPassword(ctx context.Context, sqlDB *sql.DB, username, passwordHash string) error {
	q := repository.New(sqlDB)
	existing, err := q.GetAppUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user not found: %s", username)
		}
		return fmt.Errorf("lookup app_user: %w", err)
	}
	if existing.AuthType != "local" {
		return fmt.Errorf("cannot reset password for user %q: auth_type=%q (password is managed by AD; reset on the AD side)", username, existing.AuthType)
	}
	hash := passwordHash
	affected, err := q.UpdateAppUserPasswordHash(ctx, repository.UpdateAppUserPasswordHashParams{
		PasswordHash: &hash,
		Username:     username,
	})
	if err != nil {
		return fmt.Errorf("update password_hash: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

// readPassword は env → stdin プロンプトの順でパスワードを取得する。
//
//   - envInitialPassword が設定されていればその値を採用 (プロンプト省略)
//   - 未設定で stdin が *os.File かつ TTY なら term.ReadPassword で
//     エコー抑制し、確認のため 2 回入力させて一致確認
//   - 未設定で stdin が *os.File かつ非 TTY (パイプ) ならエラー
//     (空入力でアカウント作成を防ぐ)
//   - 未設定で stdin が *os.File でない (テストの bytes.Buffer 等) は
//     bufio.Scanner で 1 行ずつ読んで 2 回一致確認 (テスト容易性のため)
//
// いずれの経路でも MinPasswordLength 未満なら auth.ErrPasswordTooShort。
func readPassword(stdin io.Reader, stdout io.Writer, getenv func(string) string) (string, error) {
	if pw := getenv(envInitialPassword); pw != "" {
		if len(pw) < auth.MinPasswordLength {
			return "", auth.ErrPasswordTooShort
		}
		return pw, nil
	}

	// stdin が TTY な *os.File なら term.ReadPassword でエコー抑制
	if f, ok := stdin.(*os.File); ok {
		if term.IsTerminal(int(f.Fd())) {
			return readPasswordFromTerm(f, stdout)
		}
		// 非 TTY (パイプ・リダイレクト) で env も未設定 → エラー
		return "", fmt.Errorf("set %s env or run from a TTY (stdin is not a terminal)", envInitialPassword)
	}

	// テスト等の non-File stdin: bufio.Scanner で 2 回読み
	return readPasswordFromScanner(stdin, stdout)
}

func readPasswordFromTerm(f *os.File, stdout io.Writer) (string, error) {
	_, _ = fmt.Fprint(stdout, "Password: ")
	first, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(stdout) // 改行 (ReadPassword は echo しない)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	_, _ = fmt.Fprint(stdout, "Password (again): ")
	second, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(stdout)
	if err != nil {
		return "", fmt.Errorf("read password (again): %w", err)
	}
	return finalizePassword(string(first), string(second))
}

func readPasswordFromScanner(stdin io.Reader, stdout io.Writer) (string, error) {
	scanner := bufio.NewScanner(stdin)
	_, _ = fmt.Fprint(stdout, "Password: ")
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return "", errors.New("read password: empty input")
	}
	first := scanner.Text()
	_, _ = fmt.Fprint(stdout, "Password (again): ")
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read password (again): %w", err)
		}
		return "", errors.New("read password (again): empty input")
	}
	second := scanner.Text()
	return finalizePassword(first, second)
}

func finalizePassword(first, second string) (string, error) {
	if first != second {
		return "", errors.New("passwords do not match")
	}
	if len(first) < auth.MinPasswordLength {
		return "", auth.ErrPasswordTooShort
	}
	return first, nil
}

// joinRoles は []middleware.Role を " / " 区切りの文字列に整形する。
// CLI のエラーメッセージで「許可されたロール」を提示する用途。
func joinRoles(roles []middleware.Role) string {
	parts := make([]string, len(roles))
	for i, r := range roles {
		parts[i] = string(r)
	}
	return strings.Join(parts, " / ")
}

// nullableString は空文字を NULL (= nil) として扱うヘルパ。
// notify_email や department_code の「未指定」を sqlc の *string に
// マッピングするときに使う。
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
