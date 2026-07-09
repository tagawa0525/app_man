# 部署スコープ認可の負債解消 (仕様 §7.2)

## Context

L-1 以降「license_manager の部署スコープ制限は継続負債 (AD 連携前に
認可強化 PR)」として先送りしてきた項目。AuthMiddleware は
ListActiveRolesForAppUser の結果から最高ロールだけを context に載せ、
ハンドラはロール階層でしか認可していないため、部署別ロール
(license_manager / dept_security_admin) が**他部署のデータを操作できる**。
AD 連携で部署別ロールの実利用者が入る前に §7.2 の「データの所属部署を
照らして判定」を実装する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| context の拡張 | AuthMiddleware が既に取得している全ロール行 (role, department_id) を `RoleGrantsFrom(ctx)` として公開 (既存 RoleFrom = 最高ロールは互換維持) | 追加クエリなし (既取得データの公開のみ)。表示の出し分けは従来の最高ロールで十分 |
| 判定ヘルパ | `middleware.HasDepartmentRole(ctx, minRole Role, deptID int64) bool` — system_admin は常に true。それ以外は「minRole 以上のロール行で department_id が一致 or NULL (全社スコープ行)」 | §7.2 の判定を 1 箇所に集約。「以上」は securityAdmins/editors 束と同じ包含集合で解釈 |
| 適用対象 (書込み系のみ) | licenses: create (owning dept) / update (現部署と変更先の両方) / 割当追加・解除 / 証書アップロード / キー閲覧 — license_manager 相当。approvals: 登録・取消 — dept_security_admin 相当 | §7.1 の「自部署の」が付く権限。閲覧系 (一覧・詳細・ダウンロード) は §5.6 の全社開示方針と L-1 判断を維持して変更しない |
| 違反時 | 403 (既存 RequireRole の 403 と同じ体裁) | ルート gate (ロール階層) を通過した後の第 2 層 |
| /admin/* と全社系 | 変更なし (system_admin 束のみで完結) | 部署概念がない |
| dept_security_admin と licenses | licenses 書込みは editors 束に含まれる dept_security_admin にも部署スコープを適用 | 「以上」ロールでも自部署制限は同じ (§7.1 の表で「自部署」列) |

## 対象スコープ

- middleware: RoleGrants の context 公開 + HasDepartmentRole + テスト
- web: licenses.go / assignments.go / documents.go / approvals.go の
  書込みハンドラに部署チェック追加
- handler テスト: 「他部署の license_manager が 403 / 自部署は成功 /
  system_admin は常に成功」を各書込み経路で

### 範囲外

- 閲覧系の部署絞り込み (§5.6 の全社開示方針)
- general_user のセルフサービス制限 (AD 後のフェーズ 5)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `test(middleware): RoleGrants 公開と HasDepartmentRole (RED)`
3. `feat(middleware): 部署スコープ判定の基盤 (GREEN)`
4. `test(web): 他部署ロールの書込みが 403 (RED)`
5. `feat(web): licenses/approvals 書込みに部署スコープ認可 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: 部署 A の license_manager が (a) 部署 B 所管ライセンスの
  編集・割当・証書・キー閲覧 → 403 (b) 部署 B を所管にした新規作成 →
  403 (c) 自部署 A では全操作成功。部署 A の dept_security_admin が
  部署 B の承認登録 → 403。system_admin は全部署で成功
- 既存テスト (system_admin / 単一部署ロールで書いたもの) が緑のまま

## 想定リスク

- **既存 handler テストの前提**: AuthenticatedPostForm 等のヘルパが
  ロール行をどう作っているか (department_id 付きか) に依存。全社
  スコープ行 (department_id NULL) で作られていれば挙動不変、部署付き
  なら対象部署に合わせる修正が要る — 実装時に確認して対応
