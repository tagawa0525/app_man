package handlertest_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
)

func TestAssertContains(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	_, _ = rec.WriteString("hello world")
	handlertest.AssertContains(t, rec, "world")
}

func TestAssertRedirect(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	rec.Header().Set("Location", "/next")
	rec.WriteHeader(http.StatusSeeOther)
	handlertest.AssertRedirect(t, rec, "/next")
}
