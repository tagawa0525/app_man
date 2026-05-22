package web

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/tagawa0525/app_man/internal/repository"
)

// resolvePinnedDepartment は depts (現役) に含まれていない参照先を 1 件
// fetch して返す。編集中レコードが廃止済み部署を指している場合に、その
// option を select に残すために使う (PinnedOption 単一 ID 版)。
//
// id が nil もしくは既に depts に含まれる場合は nil。
// sql.ErrNoRows は nil 扱い (整合性崩れの保護)。
//
// PR-D (users) と PR-E (devices) で 2 度複製した後、本 PR (PR-E) 末尾の
// リファクタで集約した。departments.go の resolvePinnedDepartments
// (複数 ID 可変長引数版) とはシグネチャが違うため別関数として共存させる。
func resolvePinnedDepartment(r *http.Request, q *repository.Queries, depts []repository.Department, id *int64) (*repository.Department, error) {
	if id == nil {
		return nil, nil
	}
	for _, d := range depts {
		if d.ID == *id {
			return nil, nil
		}
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

// asciiCodeRE はコード系フィールド (employee_code / 部署 code /
// asset_code 等) の受け付け可能文字。AD 連携キーや資産台帳キーとして
// 利用される想定で、英数 + ハイフン + アンダースコアのみ。
var asciiCodeRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateASCIICode はコード系フィールドの共通検証。
// 必須、1〜maxLen 文字、^[A-Za-z0-9_-]+$。label は日本語のフィールド名
// (例: "従業員コード" / "部署コード" / "資産コード") を渡し、メッセージに
// 直接埋め込む。
//
// users・departments・devices で 3 度登場した時点で集約 (CLAUDE.md
// 「3 回ルール」)。
func validateASCIICode(label string, maxLen int, s string) string {
	if s == "" {
		return label + "は必須です"
	}
	if utf8.RuneCountInString(s) > maxLen {
		return label + "は " + strconv.Itoa(maxLen) + " 文字以内で入力してください"
	}
	if !asciiCodeRE.MatchString(s) {
		return label + "は英数・ハイフン・アンダースコアで入力してください"
	}
	return ""
}
