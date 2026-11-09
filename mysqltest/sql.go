package mysqltest

import (
	"context"
	"database/sql"
	"time"
)

// Unwrap is an explicit escape hatch for integrations requiring *sql.DB.
// It does not transfer server ownership. Always close the owning wrapper.
func (d *Child) Unwrap() *sql.DB { return d.server.Unwrap() }

// Query forwards to the owned SQL pool.
func (d *Child) Query(query string, args ...any) (*sql.Rows, error) {
	return d.server.Query(query, args...)
}

// QueryContext forwards to the owned SQL pool.
func (d *Child) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.server.QueryContext(ctx, query, args...)
}

// QueryRow forwards to the owned SQL pool.
func (d *Child) QueryRow(query string, args ...any) *sql.Row {
	return d.server.QueryRow(query, args...)
}

// QueryRowContext forwards to the owned SQL pool.
func (d *Child) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.server.QueryRowContext(ctx, query, args...)
}

// Exec forwards to the owned SQL pool.
func (d *Child) Exec(query string, args ...any) (sql.Result, error) {
	return d.server.Exec(query, args...)
}

// ExecContext forwards to the owned SQL pool.
func (d *Child) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.server.ExecContext(ctx, query, args...)
}

// Begin forwards to the owned SQL pool.
func (d *Child) Begin() (*sql.Tx, error) { return d.server.Begin() }

// BeginTx forwards to the owned SQL pool.
func (d *Child) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.server.BeginTx(ctx, opts)
}

// Prepare forwards to the owned SQL pool.
func (d *Child) Prepare(query string) (*sql.Stmt, error) { return d.server.Prepare(query) }

// PrepareContext forwards to the owned SQL pool.
func (d *Child) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return d.server.PrepareContext(ctx, query)
}

// Ping forwards to the owned SQL pool.
func (d *Child) Ping() error { return d.server.Ping() }

// PingContext forwards to the owned SQL pool.
func (d *Child) PingContext(ctx context.Context) error { return d.server.PingContext(ctx) }

// Conn forwards to the owned SQL pool.
func (d *Child) Conn(ctx context.Context) (*sql.Conn, error) { return d.server.Conn(ctx) }

// Stats forwards to the owned SQL pool.
func (d *Child) Stats() sql.DBStats { return d.server.Stats() }

// SetMaxOpenConns forwards to the owned SQL pool.
func (d *Child) SetMaxOpenConns(n int) { d.server.SetMaxOpenConns(n) }

// SetMaxIdleConns forwards to the owned SQL pool.
func (d *Child) SetMaxIdleConns(n int) { d.server.SetMaxIdleConns(n) }

// SetConnMaxLifetime forwards to the owned SQL pool.
func (d *Child) SetConnMaxLifetime(duration time.Duration) { d.server.SetConnMaxLifetime(duration) }

// SetConnMaxIdleTime forwards to the owned SQL pool.
func (d *Child) SetConnMaxIdleTime(duration time.Duration) { d.server.SetConnMaxIdleTime(duration) }
