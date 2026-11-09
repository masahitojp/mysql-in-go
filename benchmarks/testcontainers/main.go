// Compare application-facing startup, query and clean shutdown costs.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	tc "github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	_ "github.com/testcontainers/testcontainers-go/modules/podman"
	"github.com/testcontainers/testcontainers-go/wait"

	"mysqlmem/mysql"
)

type measurement struct {
	StartMS float64 `json:"start_ms"`
	QueryMS float64 `json:"query_ms"`
	CloseMS float64 `json:"close_ms"`
	TotalMS float64 `json:"total_ms"`
	Version string  `json:"mysql_version"`
	ImageID string  `json:"image_id,omitempty"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func query(ctx context.Context, db *sql.DB) (float64, string, error) {
	begin := time.Now()
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return 0, "", err
	}
	duration := ms(time.Since(begin))
	if one != 1 {
		return 0, "", fmt.Errorf("SELECT 1: got %d, want 1", one)
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return 0, "", err
	}
	if version != "8.4.13" {
		return 0, "", fmt.Errorf("unexpected version %q (expected 8.4.13)", version)
	}
	return duration, version, nil
}
func native(ctx context.Context, root string) (m measurement, err error) {
	begin := time.Now()
	db, err := mysql.StartContext(ctx, mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets")})
	if err != nil {
		return m, err
	}
	defer db.Close()
	m.StartMS = ms(time.Since(begin))
	m.QueryMS, m.Version, err = query(ctx, db.DB)
	if err != nil {
		return m, err
	}
	stop := time.Now()
	if err := db.Close(); err != nil {
		return m, err
	}
	m.CloseMS = ms(time.Since(stop))
	m.TotalMS = ms(time.Since(begin))
	return m, nil
}
func containers(ctx context.Context, image string) (m measurement, err error) {
	begin := time.Now()
	c, err := tcmysql.Run(ctx, image, tcmysql.WithUsername("root"), tcmysql.WithPassword(""), tcmysql.WithDatabase(""),
		tc.WithEnv(map[string]string{"MYSQL_ROOT_HOST": "%"}),
		tc.WithCmdArgs("--innodb-buffer-pool-size=64M", "--mysqlx=OFF"),
		tc.WithHostConfigModifier(func(h *container.HostConfig) {
			if h.PortBindings == nil {
				h.PortBindings = network.PortMap{}
			}
			for _, name := range []string{"3306/tcp", "33060/tcp"} {
				p, err := network.ParsePort(name)
				if err != nil {
					panic(err)
				}
				h.PortBindings[p] = []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1")}}
			}
		}),
		tc.WithWaitStrategy(wait.ForSQL("3306/tcp", "mysql", func(host string, p network.Port) string {
			return "root@tcp(" + net.JoinHostPort(host, p.Port()) + ")/?timeout=1s"
		}).WithPollInterval(50*time.Millisecond).WithStartupTimeout(2*time.Minute)),
	)
	terminated := false
	if c != nil {
		defer func() {
			if terminated {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if e := c.Terminate(cleanupCtx); err == nil && e != nil {
				err = e
			}
		}()
	}
	if err != nil {
		return m, err
	}
	dsn, err := c.ConnectionString(ctx, "timeout=1s")
	if err != nil {
		return m, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return m, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return m, err
	}
	m.StartMS = ms(time.Since(begin))
	m.QueryMS, m.Version, err = query(ctx, db)
	if err != nil {
		return m, err
	}
	info, err := c.Inspect(ctx)
	if err != nil {
		return m, err
	}
	m.ImageID = info.Image
	stop := time.Now()
	if err := db.Close(); err != nil {
		return m, err
	}
	timeout := 30 * time.Second
	if err := c.Stop(ctx, &timeout); err != nil {
		return m, err
	}
	state, err := c.State(ctx)
	if err != nil {
		return m, err
	}
	if state.ExitCode != 0 {
		return m, fmt.Errorf("MySQL container exited with %d", state.ExitCode)
	}
	if err := c.Terminate(ctx); err != nil {
		return m, err
	}
	terminated = true
	m.CloseMS = ms(time.Since(stop))
	m.TotalMS = ms(time.Since(begin))
	return m, nil
}
func run() error {
	runs := flag.Int("runs", 5, "measured samples per implementation")
	image := flag.String("image", "docker.io/library/mysql:8.4.13", "pre-pulled MySQL image")
	root := flag.String("root", "../..", "mysql-in-go source root")
	flag.Parse()
	if *runs < 1 {
		return fmt.Errorf("runs must be positive")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	fmt.Fprintln(os.Stderr, "Unmeasured Testcontainers warm-up (includes Ryuk and any missing helper image)")
	if _, err := containers(ctx, *image); err != nil {
		return fmt.Errorf("warm-up: %w", err)
	}
	out := struct {
		Image      string        `json:"image"`
		Note       string        `json:"note"`
		Native     []measurement `json:"bao"`
		Containers []measurement `json:"testcontainers_podman"`
	}{Image: *image, Note: "Alternating order; fresh DB every run; images and Go build excluded; rootless Podman; Ryuk stays enabled and is warmed once. Bao uses Unix socket/temp files; official MySQL image uses TCP/a fresh Podman-managed volume (image storage driver: overlay). Both buffer pools 64 MiB, mysqlx disabled; this is end-to-end setup comparison, not an isolated Bao benchmark."}
	for i := 0; i < *runs; i++ {
		functions := []struct {
			name   string
			run    func() (measurement, error)
			target *[]measurement
		}{
			{"Bao", func() (measurement, error) { return native(ctx, abs) }, &out.Native},
			{"Testcontainers", func() (measurement, error) { return containers(ctx, *image) }, &out.Containers},
		}
		if i%2 == 1 {
			functions[0], functions[1] = functions[1], functions[0]
		}
		for _, f := range functions {
			m, err := f.run()
			if err != nil {
				return fmt.Errorf("%s run %d: %w", f.name, i+1, err)
			}
			*f.target = append(*f.target, m)
			fmt.Fprintf(os.Stderr, "%s %d/%d: start %.1f ms, query %.3f ms, close %.1f ms, total %.1f ms\n", f.name, i+1, *runs, m.StartMS, m.QueryMS, m.CloseMS, m.TotalMS)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
