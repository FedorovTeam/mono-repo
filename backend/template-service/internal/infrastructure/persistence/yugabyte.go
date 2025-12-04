package persistence

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"template-service/pkg/config"
	"time"
)

func InitYugabyteDB(cfg config.YugabyteConfig) (*sql.DB, error) {
	// Параметры безопасности и производительности
	options := "-c statement_timeout=15000 -c idle_in_transaction_session_timeout=30000"

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=5 options='%s'",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DB, cfg.SSLMode, options,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open DB: %w", err)
	}

	// Проверка соединения
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping DB: %w", err)
	}

	// Настройки пула соединений
	db.SetMaxOpenConns(50)                  // максимум открытых соединений
	db.SetMaxIdleConns(10)                  // сколько держать "в запасе"
	db.SetConnMaxLifetime(10 * time.Minute) // время жизни соединения
	db.SetConnMaxIdleTime(2 * time.Minute)  // макс. время в idle

	return db, nil
}
