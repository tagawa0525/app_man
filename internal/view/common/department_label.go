// Package common は複数の view パッケージで共有する純粋関数を集める。
//
// 個別パッケージ (users / devices / 等) で 3 度以上重複した整形ロジックを
// 本パッケージに集約する。CLAUDE.md「3 回重複してから抽象化」原則に従い、
// 2 度目までは個別パッケージ内に複製する。
package common

import (
	"github.com/tagawa0525/app_man/internal/repository"
)

// DepartmentLabel は所属部署列 / show リンク / form select 等で共通利用する
// 部署ラベル。廃止部署は "営業部 (〜2026-04-01)" のように廃止日を併記する。
// users (PR-D)・devices (PR-E) で 2 度複製したため、3 度目の整理として
// 本パッケージに集約した。
func DepartmentLabel(d repository.Department) string {
	if d.ValidTo != nil {
		return d.Name + " (〜" + d.ValidTo.Format("2006-01-02") + ")"
	}
	return d.Name
}
