package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/masahitojp/mariamem/mariamemtest"
)

type sample struct {
	PreparationMS float64   `json:"preparation_ms"`
	ChildrenMS    []float64 `json:"children_ms"`
	CleanupMS     float64   `json:"cleanup_ms"`
	TotalMS       float64   `json:"total_ms"`
	Version       string    `json:"version"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

var children = flag.Int("children", 5, "children per prepared fixture")

func TestTiming(t *testing.T) {
	var samples []sample
	for run := 0; run < 5; run++ {
		var s sample
		begin := time.Now()
		f, err := mariamemtest.New(t.Context(), mariamemtest.Options{MaxForks: 2, Prepare: func(ctx context.Context, db *sql.DB, _ string) error {
			_, err := db.ExecContext(ctx, "CREATE TABLE seed(id INT PRIMARY KEY) ENGINE=InnoDB")
			return err
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		})
		s.PreparationMS = ms(time.Since(begin))
		for i := 0; i < *children; i++ {
			start := time.Now()
			if !t.Run("child", func(t *testing.T) {
				db := f.DB(t)
				var got int
				if err := db.QueryRowContext(t.Context(), "SELECT 1").Scan(&got); err != nil || got != 1 {
					t.Fatalf("SELECT 1=%d %v", got, err)
				}
				if err := db.QueryRowContext(t.Context(), "SELECT VERSION()").Scan(&s.Version); err != nil {
					t.Fatal(err)
				}
			}) {
				t.Fatal("child failed")
			}
			s.ChildrenMS = append(s.ChildrenMS, ms(time.Since(start)))
		}
		stop := time.Now()
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		s.CleanupMS = ms(time.Since(stop))
		s.TotalMS = ms(time.Since(begin))
		samples = append(samples, s)
	}
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/mariamem-timing.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
}
