// Package mysql provides disposable MySQL 8.4 servers for Go tests.
// Start downloads a pinned server bundle on first use outside a source checkout.
// The server runs as a separate child process; Close leaves the caller running.
package mysql

import (
	"context"
	"github.com/masahitojp/mysql-in-go/internal/serverbundle"
	"github.com/masahitojp/mysql-in-go/mysql"
)

type Options = mysql.Options
type DB = mysql.DB

func Start(options ...Options) (*DB, error) { return mysql.Start(options...) }
func StartContext(ctx context.Context, options ...Options) (*DB, error) {
	return mysql.StartContext(ctx, options...)
}

// Prefetch downloads and verifies the pinned server without starting MySQL.
// Subsequent Start calls use the cached bundle, including when offline.
func Prefetch(ctx context.Context) error { _, err := serverbundle.Resolve(ctx); return err }
