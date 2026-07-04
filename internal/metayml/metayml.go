// Package metayml は契約フォルダの meta.yml (仕様 §5.2) を生成する。
// システム廃止時にファイルサーバを覗くだけで全情報が読める状態を保つ
// ため、仕様の例と同キー順・同形式で出力する (FS が正本、DB は検索
// インデックス)。日時は JST の ISO8601 オフセット表記 (§8.6)。
//
// yaml.v3 の struct Marshal は nil を null、日付風文字列を引用符付きで
// 出すため仕様の見た目 (「キー: のみ」の空値・引用符なしの日付) と
// 一致しない。yaml.Node を直接組み立ててスタイルを制御する。
package metayml

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// header は自動生成ヘッダコメント (仕様 §5.2 の例の先頭 2 行)。
const header = "# このファイルは本システムが自動生成しています\n" +
	"# 手動編集は次回同期時に上書きされます\n"

// jst は meta.yml の日時表記に使うタイムゾーン (§8.6)。
var jst = time.FixedZone("JST", 9*3600)

// Meta は meta.yml の内容。フィールド順 = 出力キー順 (仕様 §5.2)。
type Meta struct {
	Product          string
	Vendor           string
	Edition          *string
	LicenseSlug      string
	DisplayName      string
	TotalCount       *int64
	CountUnit        string
	ContractType     string
	PurchasedAt      *time.Time
	StartedAt        *time.Time
	ExpiresAt        *time.Time
	OwningDepartment string
	VendorOrderNo    *string
	Purchaser        *string
	UnitPrice        *int64
	Currency         *string
	Documents        []Document
	Note             *string
	LastUpdatedByApp time.Time
}

// Document は documents 配下の 1 エントリ。
type Document struct {
	Filename   string
	SHA256     string
	UploadedAt time.Time
}

// Write は m を仕様 §5.2 の形式で path に書く。同一ディレクトリの一時
// ファイルに書いてから rename する (書きかけの meta.yml を読ませない。
// 同一ボリューム内 rename なので原子的)。
func Write(path string, m Meta) error {
	data, err := render(m)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".meta-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp meta file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp meta file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp meta file to %s: %w", path, err)
	}
	return nil
}

// render はヘッダコメント + YAML 本文のバイト列を組み立てる。
func render(m Meta) ([]byte, error) {
	docs := &yaml.Node{Kind: yaml.SequenceNode, Style: 0}
	if len(m.Documents) == 0 {
		// 空は仕様の見た目に合わせて documents: [] のフロー表記
		docs.Style = yaml.FlowStyle
	}
	for _, d := range m.Documents {
		docs.Content = append(docs.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			strNode("filename"), strNode(d.Filename),
			strNode("sha256"), strNode(d.SHA256),
			strNode("uploaded_at"), datetimeNode(d.UploadedAt),
		}})
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		strNode("product"), strNode(m.Product),
		strNode("vendor"), strNode(m.Vendor),
		strNode("edition"), optStrNode(m.Edition),
		strNode("license_slug"), strNode(m.LicenseSlug),
		strNode("display_name"), strNode(m.DisplayName),
		strNode("total_count"), optIntNode(m.TotalCount),
		strNode("count_unit"), strNode(m.CountUnit),
		strNode("contract_type"), strNode(m.ContractType),
		strNode("purchased_at"), optDateNode(m.PurchasedAt),
		strNode("started_at"), optDateNode(m.StartedAt),
		strNode("expires_at"), optDateNode(m.ExpiresAt),
		strNode("owning_department"), strNode(m.OwningDepartment),
		strNode("vendor_order_no"), optStrNode(m.VendorOrderNo),
		strNode("purchaser"), optStrNode(m.Purchaser),
		strNode("unit_price"), optIntNode(m.UnitPrice),
		strNode("currency"), optStrNode(m.Currency),
		strNode("documents"), docs,
		strNode("note"), noteNode(m.Note),
		strNode("last_updated_by_app"), datetimeNode(m.LastUpdatedByApp),
	}}

	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encode meta yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// nullNode は「キー: のみ」の空値を出す (yaml.v3 の既定は null 表記の
// ため !!null タグ + 空 Value で明示する)。
func nullNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
}

func optStrNode(s *string) *yaml.Node {
	if s == nil {
		return nullNode()
	}
	return strNode(*s)
}

func optIntNode(i *int64) *yaml.Node {
	if i == nil {
		return nullNode()
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", *i)}
}

// optDateNode は日付を YYYY-MM-DD で出す。DATE 列はタイムゾーンを持たない
// 暦日なので変換せずそのまま整形する。!!timestamp タグにより引用符なしの
// プレーン表記になる (仕様の例と同形式)。
func optDateNode(t *time.Time) *yaml.Node {
	if t == nil {
		return nullNode()
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: t.Format("2006-01-02")}
}

// datetimeNode は日時を JST の ISO8601 オフセット表記で出す (§8.6)。
func datetimeNode(t time.Time) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: t.In(jst).Format("2006-01-02T15:04:05-07:00")}
}

// noteNode は note を出す。複数行はリテラルブロック (|) 表記 (仕様の例)。
func noteNode(s *string) *yaml.Node {
	if s == nil {
		return nullNode()
	}
	n := strNode(*s)
	if len(*s) > 0 && (*s)[len(*s)-1] == '\n' {
		n.Style = yaml.LiteralStyle
	}
	return n
}
