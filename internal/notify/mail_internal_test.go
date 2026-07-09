package notify

import (
	"strings"
	"testing"
)

// 本文が既に CRLF を含む場合に \r\r\n へ壊れないこと (LF へ正規化して
// から CRLF 変換する)。PR #37 Copilot 指摘。
func TestBuildMailMessage_NormalizesExistingCRLF(t *testing.T) {
	t.Parallel()
	msg := string(buildMailMessage("from@x", Notification{
		ID: 1, Recipient: "to@x", Subject: "s", Body: "line1\r\nline2\nline3",
	}))
	if strings.Contains(msg, "\r\r\n") {
		t.Errorf("message contains \\r\\r\\n (double conversion):\n%q", msg)
	}
	if !strings.Contains(msg, "line1\r\nline2\r\nline3") {
		t.Errorf("body lines should be CRLF-separated exactly once:\n%q", msg)
	}
}
