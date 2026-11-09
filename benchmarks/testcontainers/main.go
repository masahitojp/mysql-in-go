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
	StartMS      float64 `json:"start_ms"`
	QueryMS      float64 `json:"query_ms"`
	CloseMS      float64 `json:"close_ms"`
	TotalMS      float64 `json:"total_ms"`
	Version      string  `json:"mysql_version"`
	ImageID      string  `json:"image_id,omitempty"`
	ShutdownMode int     `json:"innodb_fast_shutdown"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func query(ctx context.Context, db querier) (float64, string, error) {
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
func native(ctx context.Context, root string, fast bool, discard ...bool) (m measurement, err error) {
	begin := time.Now()
	db, err := mysql.StartContext(ctx, mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets"), Tmpfs: true, FastShutdown: fast})
	if err != nil {
		return m, err
	}
	defer db.Close()
	m.StartMS = ms(time.Since(begin))
	m.QueryMS, m.Version, err = query(ctx, db)
	if err != nil {
		return m, err
	}
	if err := db.QueryRowContext(ctx, "SELECT @@innodb_fast_shutdown").Scan(&m.ShutdownMode); err != nil {
		return m, err
	}
	want := 1
	if fast {
		want = 2
	}
	if m.ShutdownMode != want {
		return m, fmt.Errorf("shutdown mode %d, want %d", m.ShutdownMode, want)
	}

	stop := time.Now()
	closeDB := db.Close
	if len(discard) > 0 && discard[0] {
		closeDB = db.Discard
	}
	if err := closeDB(); err != nil {
		return m, err
	}
	m.CloseMS = ms(time.Since(stop))
	m.TotalMS = ms(time.Since(begin))
	return m, nil
}
func containers(ctx context.Context, image string, fast bool, tmpfs bool, discard ...bool) (m measurement, err error) {
	begin := time.Now()
	command := []string{"--innodb-buffer-pool-size=64M", "--mysqlx=OFF"}
	// Apply fast mode only after the entrypoint has completed initialization.
	// Passing 2 to --initialize can leave newly-created data unusable.
	storage := tc.WithTmpfs(nil)
	if tmpfs {
		storage = tc.WithTmpfs(map[string]string{"/var/lib/mysql": "rw,size=512m", "/tmp": "rw,size=64m,mode=1777"})
	}
	c, err := tcmysql.Run(ctx, image, tcmysql.WithUsername("root"), tcmysql.WithPassword(""), tcmysql.WithDatabase(""),
		tc.WithEnv(map[string]string{"MYSQL_ROOT_HOST": "%"}),
		tc.WithCmdArgs(command...), storage,
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
	if fast {
		if _, err := db.ExecContext(ctx, "SET GLOBAL innodb_fast_shutdown=2"); err != nil {
			return m, err
		}
	}
	m.StartMS = ms(time.Since(begin))
	m.QueryMS, m.Version, err = query(ctx, db)
	if err != nil {
		return m, err
	}
	if err := db.QueryRowContext(ctx, "SELECT @@innodb_fast_shutdown").Scan(&m.ShutdownMode); err != nil {
		return m, err
	}
	want := 1
	if fast {
		want = 2
	}
	if m.ShutdownMode != want {
		return m, fmt.Errorf("shutdown mode %d, want %d", m.ShutdownMode, want)
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
	force := len(discard) > 0 && discard[0]
	if force {
		timeout = 0
	}
	if err := c.Stop(ctx, &timeout); err != nil {
		return m, err
	}
	state, err := c.State(ctx)
	if err != nil {
		return m, err
	}
	if (!force && state.ExitCode != 0) || (force && state.ExitCode != 0 && state.ExitCode != 137) {
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
	containerTmpfs := flag.Bool("container-tmpfs", true, "put container datadir and SQL temp files on tmpfs to match the native API")
	disposable := flag.Bool("disposable", false, "compare fresh/disposable native, prepared fixture and container stop timeout zero")
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
	if _, err := containers(ctx, *image, false, *containerTmpfs); err != nil {
		return fmt.Errorf("warm-up: %w", err)
	}
	if *disposable {
		if !*containerTmpfs {
			return fmt.Errorf("disposable comparison requires container-tmpfs")
		}
		return compareDisposable(ctx, abs, *image, *runs)
	}
	out := struct {
		Image          string        `json:"image"`
		Note           string        `json:"note"`
		Native         []measurement `json:"bao"`
		Containers     []measurement `json:"testcontainers_podman"`
		NativeFast     []measurement `json:"bao_fast_shutdown"`
		ContainersFast []measurement `json:"testcontainers_podman_fast_shutdown"`
		ContainerTmpfs bool          `json:"container_tmpfs"`
	}{Image: *image, ContainerTmpfs: *containerTmpfs, Note: "Four variants in rotated order, fresh DB every run. Native datadir and SQL tmpdir are verified tmpfs. Container storage is selected by container_tmpfs; tmpfs sizes are 512 MiB data / 64 MiB temp. Regular shutdown uses default innodb_fast_shutdown=1, fast mode uses 2 only after data initialization (container SET GLOBAL after entrypoint readiness). SQL/connection readiness polled every 50 ms; SELECT 1 and MySQL 8.4.13 asserted. Images/Go build and one Ryuk warm-up excluded. Buffer pools 64 MiB. Native uses Unix socket; official image uses TCP and different entrypoint/build. End-to-end setup comparison, not isolated Bao performance."}
	for i := 0; i < *runs; i++ {
		functions := []struct {
			name   string
			run    func() (measurement, error)
			target *[]measurement
		}{
			{"Bao", func() (measurement, error) { return native(ctx, abs, false) }, &out.Native},
			{"Bao-fast", func() (measurement, error) { return native(ctx, abs, true) }, &out.NativeFast},
			{"Testcontainers", func() (measurement, error) { return containers(ctx, *image, false, *containerTmpfs) }, &out.Containers},
			{"Testcontainers-fast", func() (measurement, error) { return containers(ctx, *image, true, *containerTmpfs) }, &out.ContainersFast},
		}
		offset := i % len(functions)
		functions = append(functions[offset:], functions[:offset]...)
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
