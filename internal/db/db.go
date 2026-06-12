package db

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

const sqliteDriverName = "agentledger_sqlite3"

type Database struct {
	conn *sql.DB
	path string
}

func init() {
	sql.Register(sqliteDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("agentledger_project_label", projectLabel, true)
		},
	})
}

func Open(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON", path)
	conn, err := sql.Open(sqliteDriverName, dsn)
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

func projectLabel(projectPath any) string {
	var value string
	switch typed := projectPath.(type) {
	case nil:
		value = ""
	case string:
		value = typed
	case []byte:
		value = string(typed)
	default:
		value = fmt.Sprint(typed)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "no-project"
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "no-project"
	}
	if strings.Contains(normalized, "/") {
		base := path.Base(normalized)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	return normalized
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
