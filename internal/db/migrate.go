package db

import "database/sql"

// MigrateUp は embed された全マイグレーションを順に適用する。
// 既に最新版で何も変わらない場合もエラーを返さない。
func MigrateUp(sqlDB *sql.DB) error {
	_ = sqlDB
	panic("not implemented")
}

// MigrateDown は適用済みマイグレーションを逆順に巻き戻し、
// すべての DDL を DROP する。
func MigrateDown(sqlDB *sql.DB) error {
	_ = sqlDB
	panic("not implemented")
}
