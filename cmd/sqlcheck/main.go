// sqlcheck exercises SQL behavior against the Bao-linked server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	phase := flag.String("phase", "setup", "setup or verify after server restart")
	flag.Parse()
	if err := check(*phase); err != nil {
		log.Fatal(err)
	}
}

func check(phase string) error {
	db, err := sql.Open("mysql", os.Getenv("MYSQL_DSN"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	exec := func(query string, args ...any) error { _, err := db.ExecContext(ctx, query, args...); return err }
	expect := func(label, query, want string, args ...any) error {
		var got string
		if err := db.QueryRowContext(ctx, query, args...).Scan(&got); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if got != want {
			return fmt.Errorf("%s: got %q, want %q", label, got, want)
		}
		fmt.Printf("PASS %s: %s\n", label, got)
		return nil
	}
	if phase == "setup" {
		for _, query := range []string{
			"CREATE DATABASE demo",
			"CREATE TABLE demo.users (id BIGINT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(100) NOT NULL, points INT NOT NULL DEFAULT 0, profile JSON, INDEX idx_points(points)) ENGINE=InnoDB",
			"CREATE TABLE demo.orders (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, amount INT NOT NULL, FOREIGN KEY (user_id) REFERENCES demo.users(id)) ENGINE=InnoDB",
		} {
			if err := exec(query); err != nil {
				return err
			}
		}
		fmt.Println("PASS CREATE DATABASE / TABLE / INDEX / FOREIGN KEY")
		stmt, err := db.PrepareContext(ctx, "INSERT INTO demo.users(name, points, profile) VALUES (?, ?, ?)")
		if err != nil {
			return err
		}
		for _, row := range []struct {
			name    string
			points  int
			profile string
		}{{"alice", 10, `{"city":"Tokyo","active":true}`}, {"bob", 20, `{"city":"Osaka","active":false}`}, {"O'Reilly; SELECT 999", 0, `{}`}} {
			if _, err := stmt.ExecContext(ctx, row.name, row.points, row.profile); err != nil {
				stmt.Close()
				return err
			}
		}
		stmt.Close()
		if err := expect("prepared INSERT / ordered SELECT", "SELECT GROUP_CONCAT(CONCAT(id, ':', name, ':', points) ORDER BY id SEPARATOR '|') FROM demo.users", "1:alice:10|2:bob:20|3:O'Reilly; SELECT 999:0"); err != nil {
			return err
		}
		if err := expect("SUM", "SELECT SUM(points) FROM demo.users", "30"); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE demo.users SET points=points+100 WHERE id=1"); err != nil {
			tx.Rollback()
			return err
		}
		var inside int
		if err := tx.QueryRowContext(ctx, "SELECT points FROM demo.users WHERE id=1").Scan(&inside); err != nil {
			tx.Rollback()
			return err
		}
		if inside != 110 {
			tx.Rollback()
			return fmt.Errorf("transaction read: got %d, want 110", inside)
		}
		if err := tx.Rollback(); err != nil {
			return err
		}
		if err := expect("ROLLBACK", "SELECT points FROM demo.users WHERE id=1", "10"); err != nil {
			return err
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE demo.users SET points=points+5 WHERE id=2"); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := expect("COMMIT", "SELECT points FROM demo.users WHERE id=2", "25"); err != nil {
			return err
		}
		if err := exec("INSERT INTO demo.orders VALUES (1,1,120),(2,1,80),(3,2,50)"); err != nil {
			return err
		}
		if err := expect("JOIN / GROUP BY", "SELECT GROUP_CONCAT(CONCAT(name,':',total) ORDER BY name) FROM (SELECT u.name,SUM(o.amount) total FROM demo.users u JOIN demo.orders o ON o.user_id=u.id GROUP BY u.id,u.name) q", "alice:200,bob:50"); err != nil {
			return err
		}
		if err := exec("UPDATE demo.users SET profile=JSON_SET(profile,'$.visits',3) WHERE id=1"); err != nil {
			return err
		}
		if err := expect("JSON read / update", "SELECT CONCAT(profile->>'$.city',':',profile->>'$.visits') FROM demo.users WHERE id=1", "Tokyo:3"); err != nil {
			return err
		}
		if err := expect("window ROW_NUMBER", "SELECT GROUP_CONCAT(CONCAT(name,':',rn) ORDER BY rn) FROM (SELECT name,ROW_NUMBER() OVER (ORDER BY points DESC,id) rn FROM demo.users) q", "bob:1,alice:2,O'Reilly; SELECT 999:3"); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, "EXPLAIN SELECT id FROM demo.users FORCE INDEX(idx_points) WHERE points>=20")
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		values := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if !rows.Next() {
			rows.Close()
			return fmt.Errorf("EXPLAIN returned no rows")
		}
		if err := rows.Scan(ptrs...); err != nil {
			rows.Close()
			return err
		}
		key := ""
		access := ""
		for i, c := range cols {
			if c == "key" {
				key = values[i].String
			}
			if c == "type" {
				access = values[i].String
			}
		}
		rows.Close()
		if key != "idx_points" || access != "range" {
			return fmt.Errorf("EXPLAIN: key=%s type=%s", key, access)
		}
		fmt.Println("PASS EXPLAIN: key=idx_points type=range (FORCE INDEX)")
		for _, negative := range []struct {
			label, query string
			code         uint16
		}{
			{"duplicate primary key", "INSERT INTO demo.orders VALUES (1,1,999)", 1062},
			{"foreign key violation", "INSERT INTO demo.orders VALUES (4,999,1)", 1452},
		} {
			err := exec(negative.query)
			var me *mysql.MySQLError
			if !errors.As(err, &me) || me.Number != negative.code {
				return fmt.Errorf("%s: expected MySQL %d, got %v", negative.label, negative.code, err)
			}
			fmt.Printf("PASS %s rejected: MySQL %d\n", negative.label, me.Number)
		}
		if err := exec("DELETE FROM demo.users WHERE id=3"); err != nil {
			return err
		}
		if err := expect("DELETE", "SELECT COUNT(*) FROM demo.users", "2"); err != nil {
			return err
		}
	} else if phase == "verify" {
		for _, c := range []struct{ label, query, want string }{
			{"restart: committed rows", "SELECT GROUP_CONCAT(CONCAT(name,':',points) ORDER BY id) FROM demo.users", "alice:10,bob:25"},
			{"restart: JSON", "SELECT profile->>'$.visits' FROM demo.users WHERE id=1", "3"},
			{"restart: orders / rejected inserts", "SELECT CONCAT(COUNT(*),':',SUM(amount)) FROM demo.orders", "3:250"},
		} {
			if err := expect(c.label, c.query, c.want); err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("unknown phase %q", phase)
	}
	return exec("SHUTDOWN")
}
