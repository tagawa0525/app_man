package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/auth"
)

func TestHash_RoundTrip(t *testing.T) {
	t.Parallel()

	const plaintext = "correct-horse-battery-staple"

	hash, err := auth.Hash(plaintext)
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("Hash returned empty string")
	}
	if strings.Contains(hash, plaintext) {
		t.Fatalf("Hash leaked plaintext: %q", hash)
	}

	if err := auth.Verify(hash, plaintext); err != nil {
		t.Fatalf("Verify with correct password failed: %v", err)
	}
}

func TestHash_TooShort(t *testing.T) {
	t.Parallel()

	_, err := auth.Hash("short")
	if !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("Hash with 5-char password: want ErrPasswordTooShort, got %v", err)
	}

	_, err = auth.Hash("")
	if !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("Hash with empty password: want ErrPasswordTooShort, got %v", err)
	}
}

func TestVerify_Mismatch(t *testing.T) {
	t.Parallel()

	hash, err := auth.Hash("RealPassword1")
	if err != nil {
		t.Fatalf("Hash setup failed: %v", err)
	}

	err = auth.Verify(hash, "WrongPassword2")
	if !errors.Is(err, auth.ErrPasswordMismatch) {
		t.Fatalf("Verify with wrong password: want ErrPasswordMismatch, got %v", err)
	}
}

func TestVerify_InvalidHash(t *testing.T) {
	t.Parallel()

	// 形式不正のハッシュは mismatch 扱いにせず、別のエラーとして上げる
	// (bcrypt 内部のエラーをそのまま返すか、ラップする実装に任せる)。
	err := auth.Verify("not-a-bcrypt-hash", "anything")
	if err == nil {
		t.Fatal("Verify with invalid hash: want error, got nil")
	}
}

func TestMinPasswordLength_Constant(t *testing.T) {
	t.Parallel()

	if auth.MinPasswordLength != 8 {
		t.Fatalf("MinPasswordLength: want 8, got %d", auth.MinPasswordLength)
	}
}
