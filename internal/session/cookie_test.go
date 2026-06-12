package session_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/session"
)

func TestSetCookie_Attributes(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	session.SetCookie(rec, "abcdef", 8*time.Hour, false)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != session.CookieName {
		t.Errorf("Name = %q, want %q", c.Name, session.CookieName)
	}
	if c.Value != "abcdef" {
		t.Errorf("Value = %q, want %q", c.Value, "abcdef")
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want %q", c.Path, "/")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly should be true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Secure {
		t.Error("Secure should be false when secure=false")
	}
	// MaxAge は秒単位
	wantMaxAge := int((8 * time.Hour).Seconds())
	if c.MaxAge != wantMaxAge {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, wantMaxAge)
	}
}

func TestSetCookie_SecureFlag(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	session.SetCookie(rec, "abc", time.Hour, true)

	c := rec.Result().Cookies()[0]
	if !c.Secure {
		t.Error("Secure should be true when secure=true")
	}
}

func TestClearCookie_SetsMaxAgeNegative(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	session.ClearCookie(rec, false)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != session.CookieName {
		t.Errorf("Name = %q, want %q", c.Name, session.CookieName)
	}
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative", c.MaxAge)
	}
}

func TestReadCookie_Missing(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := session.ReadCookie(r); got != "" {
		t.Fatalf("ReadCookie without cookie should return empty, got %q", got)
	}
}

func TestReadCookie_Present(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: session.CookieName, Value: "session-id-1"})

	if got := session.ReadCookie(r); got != "session-id-1" {
		t.Fatalf("ReadCookie = %q, want %q", got, "session-id-1")
	}
}
