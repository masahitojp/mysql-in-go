package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/masahitojp/mysql-in-go/mysql"
	"github.com/masahitojp/mysql-in-go/mysqltest"
)

// compareDisposable separates amortized fixture preparation from per-test costs.
func compareDisposable(ctx context.Context, root, image string, runs int) (err error) {
	begin := time.Now()
	f, err := mysqltest.New(ctx, mysqltest.Options{
		MySQL:           mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets"), Tmpfs: true},
		DiscardChildren: true,
		Prepare: func(ctx context.Context, db *sql.DB) error {
			_, err := db.ExecContext(ctx, "CREATE TABLE seed(id INT PRIMARY KEY) ENGINE=InnoDB")
			return err
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	out := struct {
		Image          string        `json:"image"`
		Note           string        `json:"note"`
		PreparationMS  float64       `json:"fixture_preparation_ms"`
		FixtureCloseMS float64       `json:"fixture_close_ms"`
		Fresh          []measurement `json:"native_fresh_discard"`
		Fixture        []measurement `json:"fixture_discard"`
		Normal         []measurement `json:"testcontainers_normal"`
		Discard        []measurement `json:"testcontainers_stop_zero"`
	}{Image: image, PreparationMS: ms(time.Since(begin)), Note: "Rotated order, five samples by default, MySQL 8.4.13 and SELECT 1 asserted, both tmpfs, buffer pool 64 MiB. Native fresh initializes each time then SIGKILL/reap/delete. Fixture copies a clean baseline with one empty InnoDB table; preparation/fixture cleanup separate. Containers initialize each time via official entrypoint; stop zero uses Stop(timeout=0), accepts exit 137 or an immediate clean exit, then Terminate/removes volumes. Normal container stop waits up to 30 s. Build/image pull/Ryuk warm-up excluded. Unix socket vs TCP and server builds/entrypoints differ. No reused-container/reset benchmark."}
	fixtureRun := func() (m measurement, err error) {
		begin := time.Now()
		c, err := f.Open(ctx)
		if err != nil {
			return m, err
		}
		defer c.Close()
		m.StartMS = ms(time.Since(begin))
		m.QueryMS, m.Version, err = query(ctx, c)
		if err != nil {
			return m, err
		}
		if err = c.QueryRowContext(ctx, "SELECT @@innodb_fast_shutdown").Scan(&m.ShutdownMode); err != nil {
			return m, err
		}
		stop := time.Now()
		if err = c.Close(); err != nil {
			return m, err
		}
		m.CloseMS = ms(time.Since(stop))
		m.TotalMS = ms(time.Since(begin))
		return m, nil
	}
	for i := 0; i < runs; i++ {
		variants := []struct {
			name   string
			run    func() (measurement, error)
			target *[]measurement
		}{
			{"Native-fresh-discard", func() (measurement, error) { return native(ctx, root, false, true) }, &out.Fresh},
			{"Fixture-discard", fixtureRun, &out.Fixture},
			{"Testcontainers-normal", func() (measurement, error) { return containers(ctx, image, false, true) }, &out.Normal},
			{"Testcontainers-stop-zero", func() (measurement, error) { return containers(ctx, image, false, true, true) }, &out.Discard},
		}
		offset := i % len(variants)
		variants = append(variants[offset:], variants[:offset]...)
		for _, v := range variants {
			m, e := v.run()
			if e != nil {
				return fmt.Errorf("%s run %d: %w", v.name, i+1, e)
			}
			*v.target = append(*v.target, m)
			fmt.Fprintf(os.Stderr, "%s %d/%d: start %.1f close %.1f total %.1f ms\n", v.name, i+1, runs, m.StartMS, m.CloseMS, m.TotalMS)
		}
	}
	stop := time.Now()
	if err = f.Close(); err != nil {
		return err
	}
	out.FixtureCloseMS = ms(time.Since(stop))
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
