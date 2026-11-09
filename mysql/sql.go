package mysql

import (
	"context"
	"database/sql"
	"time"
)

// Unwrap is an explicit escape hatch for integrations requiring *sql.DB.
// It does not transfer server ownership. Always close the owning wrapper.
func (d *DB) Unwrap() *sql.DB { return d.pool }

// Query forwards to the owned SQL pool.
func (d *DB) Query(query string, args ...any) (*sql.Rows, error) { return d.pool.Query(query, args...) }

// QueryContext forwards to the owned SQL pool.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.pool.QueryContext(ctx, query, args...)
}

// QueryRow forwards to the owned SQL pool.
func (d *DB) QueryRow(query string, args ...any) *sql.Row { return d.pool.QueryRow(query, args...) }

// QueryRowContext forwards to the owned SQL pool.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.pool.QueryRowContext(ctx, query, args...)
}

// Exec forwards to the owned SQL pool.
func (d *DB) Exec(query string, args ...any) (sql.Result, error) { return d.pool.Exec(query, args...) }

// ExecContext forwards to the owned SQL pool.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.pool.ExecContext(ctx, query, args...)
}

// Begin forwards to the owned SQL pool.
func (d *DB) Begin() (*sql.Tx, error) { return d.pool.Begin() }

// BeginTx forwards to the owned SQL pool.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.pool.BeginTx(ctx, opts)
}

// Prepare forwards to the owned SQL pool.
func (d *DB) Prepare(query string) (*sql.Stmt, error) { return d.pool.Prepare(query) }

// PrepareContext forwards to the owned SQL pool.
func (d *DB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return d.pool.PrepareContext(ctx, query)
}

// Ping forwards to the owned SQL pool.
func (d *DB) Ping() error { return d.pool.Ping() }

// PingContext forwards to the owned SQL pool.
func (d *DB) PingContext(ctx context.Context) error { return d.pool.PingContext(ctx) }

// Conn forwards to the owned SQL pool.
func (d *DB) Conn(ctx context.Context) (*sql.Conn, error) { return d.pool.Conn(ctx) }

// Stats forwards to the owned SQL pool.
func (d *DB) Stats() sql.DBStats { return d.pool.Stats() }

// SetMaxOpenConns forwards to the owned SQL pool.
func (d *DB) SetMaxOpenConns(n int) { d.pool.SetMaxOpenConns(n) }

// SetMaxIdleConns forwards to the owned SQL pool.
func (d *DB) SetMaxIdleConns(n int) { d.pool.SetMaxIdleConns(n) }

// SetConnMaxLifetime forwards to the owned SQL pool.
func (d *DB) SetConnMaxLifetime(duration time.Duration) { d.pool.SetConnMaxLifetime(duration) }

// SetConnMaxIdleTime forwards to the owned SQL pool.
func (d *DB) SetConnMaxIdleTime(duration time.Duration) { d.pool.SetConnMaxIdleTime(duration) }
