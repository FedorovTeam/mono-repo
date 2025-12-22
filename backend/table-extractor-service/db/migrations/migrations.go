package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.uber.org/zap"
)

//go:embed *.sql
var migrationFiles embed.FS

func Apply(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := migrationFiles.ReadFile(path.Join(".", entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		upMigration := extractUpMigration(string(content))
		if _, err := db.ExecContext(ctx, upMigration); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}

		if logger != nil {
			logger.Info("migration applied", zap.String("name", entry.Name()))
		}
	}

	return nil
}

func extractUpMigration(content string) string {
	const (
		upDirective   = "-- +goose Up"
		downDirective = "-- +goose Down"
	)

	if !strings.Contains(content, upDirective) {
		return content
	}

	var upSection []string
	inUpSection := false

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, upDirective):
			inUpSection = true
			continue
		case strings.HasPrefix(trimmed, downDirective):
			inUpSection = false
		}

		if inUpSection {
			upSection = append(upSection, line)
		}
	}

	if len(upSection) == 0 {
		return content
	}

	return strings.Join(upSection, "\n")
}
