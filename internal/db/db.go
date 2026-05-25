package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	conn *sql.DB
	path string
}

func Open(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON", path)
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	conn.SetMaxOpenConns(1)

	db := &Database{conn: conn, path: path}
	if err := db.initSchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

func (d *Database) Close() error {
	return d.conn.Close()
}

func (d *Database) Conn() *sql.DB {
	return d.conn
}

func (d *Database) Path() string {
	return d.path
}
