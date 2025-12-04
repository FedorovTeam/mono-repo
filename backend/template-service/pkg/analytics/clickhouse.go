package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

var ClickhouseDB *sql.DB

func InitClickhouse() error {
	dsn := fmt.Sprintf("clickhouse://%s:%s@%s/%s",
		os.Getenv("CLICKHOUSE_USER"),
		os.Getenv("CLICKHOUSE_PASSWORD"),
		os.Getenv("CLICKHOUSE_ADDR"),
		os.Getenv("CLICKHOUSE_DB"),
	)
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return err
	}
	ClickhouseDB = db
	return db.Ping()
}

func InsertEvent(ctx context.Context, event string, payload string) error {
	_, err := ClickhouseDB.ExecContext(ctx, "INSERT INTO analytics_events (event, payload, created_at) VALUES (?, ?, now())", event, payload)
	return err
}
