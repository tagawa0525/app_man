// Package bootstrap は appmgr-import-bootstrap の本体ロジックを集める。
//
// 要件書 §9 で MVP 必須と規定された CSV 一括投入バイナリのうち、本パッケージ
// では「コア機能」(検証 / dry-run / --commit / 1 トランザクション) を扱う。
// audit_logs 書き込み / alias-resolve / Shift_JIS / licenses+assignments は
// 別 PR で本パッケージに kind を足す形で拡張する想定。
package bootstrap

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"

	"github.com/tagawa0525/app_man/internal/repository"
)

// Row は CSV 1 データ行を「列名 → 値」のマップで保持する。
// Line は 1 始まり (ヘッダを除いて 1 行目から)。検証エラーの行番号通知に使う。
type Row struct {
	Line   int
	Fields map[string]string
}

// ValidationError は CSV 1 行に対する検証エラー。
// 行番号と列名を持つことで、CLI 利用者が CSV を修正しやすくする
// (要件書 §9「行番号付きで標準出力に列挙」)。
type ValidationError struct {
	Line    int
	Column  string
	Message string
}

// Importer は kind ごとの実装を抽象化する。Validate と Insert の 2 段階で、
// Validate は読み取り専用 (DB 既存レコードの引きまで)、Insert は tx 上で
// 副作用を起こす。
type Importer interface {
	// Kind はこの importer が扱う --kind 文字列 (例: "vendors")。
	Kind() string
	// HeaderColumns は CSV ヘッダ行に **完全一致** で求める列名の並び。
	// 過不足や順序違いは検証前に弾く (CSV の形が違うことを早期検知する)。
	HeaderColumns() []string
	// Validate は CSV 1 ファイル分の行を検証する。空スライスならエラーなし。
	// DB は SELECT のみ (q は非 tx の Queries を渡す想定)。
	Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError
	// Insert は Validate を通った行をトランザクション上で投入する。
	// q は WithTx 済み。件数を返す。
	Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error)
}

// Run は CSV ファイルを開き、検証 → dry-run / commit のいずれかを実行する
// dispatch 共通関数。
//
//   - csvPath を開き、ヘッダ行を importer.HeaderColumns() と突合
//   - データ行を Row に詰める
//   - importer.Validate を呼ぶ
//   - エラーがあれば out に列挙して error を返す
//   - エラーなし & dryRun=true なら「N 行検証 OK、commit しません」を出力
//   - エラーなし & dryRun=false なら BeginTx → Insert → Commit。失敗時はロールバック
//
// out は標準出力相当 (人間向けメッセージ)。構造化ログは呼び出し側で別途出す。
func Run(ctx context.Context, db *sql.DB, csvPath string, importer Importer, dryRun bool, out io.Writer) error {
	rows, err := readCSV(csvPath, importer.HeaderColumns())
	if err != nil {
		return err
	}

	q := repository.New(db)
	verrs := importer.Validate(ctx, q, rows)
	if len(verrs) > 0 {
		for _, e := range verrs {
			fmt.Fprintf(out, "line %d, column %s: %s\n", e.Line, e.Column, e.Message)
		}
		return fmt.Errorf("%d validation error(s)", len(verrs))
	}

	if dryRun {
		fmt.Fprintf(out, "%d 行検証 OK、commit しません\n", len(rows))
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		// Commit 済 tx に対する Rollback は sql.ErrTxDone を返すだけで害なし。
		// 失敗パスのフェイルセーフとして残す。
		_ = tx.Rollback()
	}()

	txQ := q.WithTx(tx)
	n, err := importer.Insert(ctx, txQ, rows)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(out, "%d 行投入\n", n)
	return nil
}

// readCSV は CSV ファイルを開き、ヘッダ行が wantHeader と完全一致することを
// 確認した上でデータ行を Row に詰める。列名は wantHeader をそのまま使う。
func readCSV(csvPath string, wantHeader []string) ([]Row, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	// ヘッダ読み取り時点では列数を強制しない。ヘッダ自体が間違ったときに
	// header mismatch のメッセージを返したいため。データ行は読み取り後に
	// FieldsPerRecord を立てて厳格に検査する。
	r.TrimLeadingSpace = false

	header, err := r.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("csv is empty (header row required)")
	}
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if !sameStringSlice(header, wantHeader) {
		return nil, fmt.Errorf("csv header mismatch: got %v, want %v", header, wantHeader)
	}
	r.FieldsPerRecord = len(wantHeader)

	var rows []Row
	for line := 1; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line %d: %w", line, err)
		}
		fields := make(map[string]string, len(wantHeader))
		for i, col := range wantHeader {
			fields[col] = rec[i]
		}
		rows = append(rows, Row{Line: line, Fields: fields})
	}
	return rows, nil
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
