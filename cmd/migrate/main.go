package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
)

const binaryName = "appmgr-migrate"

func main() {
	configPath := flag.String("config", "config.yml", "path to config.yml")
	direction := flag.String("direction", "up", "migration direction: up or down")
	flag.Parse()

	if err := run(*configPath, *direction); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		os.Exit(1)
	}
}

func run(configPath, direction string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	sqlDB, closeDB, err := db.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = closeDB() }()

	switch direction {
	case "up":
		if err := db.MigrateUp(sqlDB); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		fmt.Fprintf(os.Stdout, "%s: migrated up to version %d\n", binaryName, db.RequiredMigrationVersion())
	case "down":
		if err := db.MigrateDown(sqlDB); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		fmt.Fprintf(os.Stdout, "%s: rolled back all migrations\n", binaryName)
	default:
		return fmt.Errorf("invalid direction %q (must be 'up' or 'down')", direction)
	}
	return nil
}
