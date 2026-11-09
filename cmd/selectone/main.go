package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	shutdown := flag.Bool("shutdown", false, "shut down the test server after a successful query")
	flag.Parse()
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("MYSQL_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var one int
	var version string
	if err := db.QueryRowContext(ctx, "SELECT 1, VERSION()").Scan(&one, &version); err != nil {
		log.Fatal(err)
	}
	if one != 1 {
		log.Fatalf("SELECT 1 returned %d", one)
	}
	fmt.Printf("MySQL %s: SELECT 1 = %d\n", version, one)
	if *shutdown {
		if _, err := db.ExecContext(ctx, "SHUTDOWN"); err != nil {
			log.Fatal(err)
		}
	}
}
