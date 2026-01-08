package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"shopify-exporter/internal/config"
	"time"
)

func New(cfg config.MysqlConfig) (*sql.DB, error) {
	if cfg.Host == "" || cfg.Username == "" || cfg.Database == "" {
		return nil, fmt.Errorf("Host or Username or Database values is empty")
	}

	if cfg.Port == 0 {
		cfg.Port = 3306
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	db, err := sql.Open("mysql", dsn)

	if err != nil {
		return nil, fmt.Errorf("mysql connection error %w", err)
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(10 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errDb := db.PingContext(ctx)

	if errDb != nil {
		_ = db.Close()
		return nil, fmt.Errorf("mysql: ping %w", errDb)
	}

	return db, nil
}
