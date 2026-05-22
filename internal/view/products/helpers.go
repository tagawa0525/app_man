package products

import "strconv"

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// licenseRequiredLabel は *bool を表示用文字列にする。
func licenseRequiredLabel(p *bool) string {
	if p == nil {
		return "未判定"
	}
	if *p {
		return "必要"
	}
	return "不要"
}

// softwareTypeLabel は software_type の表示名。
func softwareTypeLabel(v string) string {
	switch v {
	case "installed":
		return "インストール型"
	case "saas":
		return "SaaS"
	case "both":
		return "両方"
	default:
		return v
	}
}

// approvalLabel は default_approval_status の表示名。
func approvalLabel(v string) string {
	switch v {
	case "globally_approved":
		return "全社承認"
	case "globally_prohibited":
		return "全社禁止"
	case "department_discretion":
		return "部署裁量"
	case "unknown":
		return "未審査"
	default:
		return v
	}
}
