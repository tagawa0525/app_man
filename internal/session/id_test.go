package session_test

import (
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/session"
)

func TestNewID_Length(t *testing.T) {
	t.Parallel()

	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID returned error: %v", err)
	}
	// crypto/rand 32 byte → base64.RawURLEncoding は 43 文字
	if got := len(id); got != 43 {
		t.Fatalf("len(NewID()) = %d, want 43", got)
	}
}

func TestNewID_URLSafe(t *testing.T) {
	t.Parallel()

	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID returned error: %v", err)
	}
	// RawURLEncoding は '+' / '/' / '=' を含まない
	if strings.ContainsAny(id, "+/=") {
		t.Fatalf("ID contains non-URL-safe character: %q", id)
	}
}

func TestNewID_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		id, err := session.NewID()
		if err != nil {
			t.Fatalf("NewID returned error at i=%d: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID collision at i=%d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewCSRFToken_Length(t *testing.T) {
	t.Parallel()

	tok, err := session.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken returned error: %v", err)
	}
	if got := len(tok); got != 43 {
		t.Fatalf("len(NewCSRFToken()) = %d, want 43", got)
	}
}

func TestNewCSRFToken_DiffersFromID(t *testing.T) {
	t.Parallel()

	// 同時に呼んでも別値であること (どちらも crypto/rand 由来)
	id, err := session.NewID()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := session.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	if id == tok {
		t.Fatalf("ID and CSRF token should differ, both = %q", id)
	}
}
