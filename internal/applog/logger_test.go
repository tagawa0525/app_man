package applog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/config"
)

func TestNew_writesJSONWithBinaryAndPID(t *testing.T) {
	dir := t.TempDir()
	cfg := config.LoggingConfig{
		Level:   "info",
		BaseDir: dir,
		Format:  "json",
	}

	logger, err := applog.New(cfg, "appmgr-test")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if logger == nil {
		t.Fatal("New() returned nil logger")
	}

	logger.Info("hello", "key", "value")

	data, err := os.ReadFile(filepath.Join(dir, "appmgr-test.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log lines written")
	}

	var got map[string]any
	last := lines[len(lines)-1]
	if err := json.Unmarshal([]byte(last), &got); err != nil {
		t.Fatalf("log line is not JSON: %v\nline=%s", err, last)
	}

	if got["binary"] != "appmgr-test" {
		t.Errorf("binary attr = %v, want appmgr-test", got["binary"])
	}
	pidRaw, ok := got["pid"]
	if !ok {
		t.Error("pid attr missing")
	} else if pid, ok := pidRaw.(float64); !ok {
		t.Errorf("pid attr = %v (%T), want number", pidRaw, pidRaw)
	} else if int(pid) != os.Getpid() {
		t.Errorf("pid attr = %d, want %d", int(pid), os.Getpid())
	}
	if got["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", got["msg"])
	}
	if got["key"] != "value" {
		t.Errorf("custom key = %v, want value", got["key"])
	}
}

func TestNew_createsLogDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deeply", "nested")
	cfg := config.LoggingConfig{
		Level:   "info",
		BaseDir: nested,
		Format:  "json",
	}

	if _, err := applog.New(cfg, "appmgr-test"); err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("expected log dir to be created: %v", err)
	}
}

func TestNew_respectsLogLevel(t *testing.T) {
	dir := t.TempDir()
	cfg := config.LoggingConfig{
		Level:   "warn",
		BaseDir: dir,
		Format:  "json",
	}

	logger, err := applog.New(cfg, "appmgr-test")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	logger.Info("info-msg")
	logger.Warn("warn-msg")

	data, err := os.ReadFile(filepath.Join(dir, "appmgr-test.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "info-msg") {
		t.Errorf("info-msg should not have been logged at warn level\nlog=%s", data)
	}
	if !strings.Contains(string(data), "warn-msg") {
		t.Errorf("warn-msg should have been logged at warn level\nlog=%s", data)
	}
}
