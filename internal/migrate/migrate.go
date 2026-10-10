package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"dnsmarty/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func Up(dsn string) error {
	return run(dsn, func(db *sql.DB) error { return goose.Up(db, "sql") })
}

func UpTo(dsn string, version int64) error {
	return run(dsn, func(db *sql.DB) error { return goose.UpTo(db, "sql", version) })
}

func DownTo(dsn string, version int64) error {
	return run(dsn, func(db *sql.DB) error { return goose.DownTo(db, "sql", version) })
}

func run(dsn string, step func(*sql.DB) error) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := step(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
