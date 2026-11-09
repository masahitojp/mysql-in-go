// startuptime measures local, sequential startup latency, excluding Go build time.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mysqlmem/mysql"
)

type sample struct {
	InitializeMS float64 `json:"initialize_ms,omitempty"`
	StartMS      float64 `json:"start_ms"`
	QueryMS      float64 `json:"query_ms"`
	CloseMS      float64 `json:"close_ms"`
	TotalMS      float64 `json:"total_ms"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func cycle(o mysql.Options) (sample, error) {
	var s sample
	begin := time.Now()
	db, err := mysql.Start(o)
	if err != nil {
		return s, err
	}
	defer db.Close()
	s.StartMS = ms(time.Since(begin))
	q := time.Now()
	var got int
	if err := db.QueryRow("SELECT 1").Scan(&got); err != nil {
		return s, err
	}
	if got != 1 {
		return s, fmt.Errorf("SELECT 1: got %d, want 1", got)
	}
	s.QueryMS = ms(time.Since(q))
	stop := time.Now()
	if err := db.Close(); err != nil {
		return s, err
	}
	s.CloseMS = ms(time.Since(stop))
	s.TotalMS = ms(time.Since(begin))
	return s, nil
}

func measure(n int) error {
	if n < 1 {
		return fmt.Errorf("runs must be positive")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "startup-timing-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	o := mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets")}
	result := struct {
		Runs     int      `json:"runs"`
		Note     string   `json:"note"`
		Fresh    []sample `json:"fresh_api"`
		Prepared []sample `json:"explicit_init_then_start"`
	}{Runs: n, Note: "Sequential local measurements; Go compilation excluded; OS caches are not cleared; Start polls readiness every 50 ms."}
	for i := 0; i < n; i++ {
		s, err := cycle(o)
		if err != nil {
			return err
		}
		result.Fresh = append(result.Fresh, s)
		fmt.Fprintf(os.Stderr, "fresh %d/%d: start %.1f ms, total %.1f ms\n", i+1, n, s.StartMS, s.TotalMS)
	}
	for i := 0; i < n; i++ {
		o.DataDir = filepath.Join(work, fmt.Sprintf("data-%d", i))
		file, err := os.Create(filepath.Join(work, fmt.Sprintf("initialize-%d.log", i)))
		if err != nil {
			return err
		}
		cmd := exec.Command(o.Binary, "--no-defaults", "--basedir="+o.BaseDir, "--datadir="+o.DataDir, "--lc-messages-dir="+filepath.Join(o.BaseDir, "share"), "--character-sets-dir="+o.CharacterSetsDir, "--initialize-insecure")
		cmd.Stdout = file
		cmd.Stderr = file
		begin := time.Now()
		err = cmd.Run()
		initMS := ms(time.Since(begin))
		file.Close()
		if err != nil {
			return fmt.Errorf("initialization: %w", err)
		}
		s, err := cycle(o)
		if err != nil {
			return err
		}
		s.InitializeMS = initMS
		s.TotalMS += initMS
		result.Prepared = append(result.Prepared, s)
		fmt.Fprintf(os.Stderr, "prepared %d/%d: initialize %.1f ms, start %.1f ms, total %.1f ms\n", i+1, n, initMS, s.StartMS, s.TotalMS)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
func main() {
	n := flag.Int("runs", 5, "samples per scenario")
	flag.Parse()
	if err := measure(*n); err != nil {
		log.Fatal(err)
	}
}
