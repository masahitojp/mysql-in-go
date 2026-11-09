package main

import (
	"fmt"
	"log"

	"mysqlmem/mysql"
)

func main() {
	db, err := mysql.Start()
	if err != nil {
		log.Fatal(err)
	}
	if err := query(db); err != nil {
		_ = db.Close()
		log.Fatal(err)
	}
	if err := db.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("MySQL を終了しました。Go の処理は続いています。")
}

func query(db *mysql.DB) error {
	rows, err := db.Query("SELECT 1")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("SELECT 1 returned no rows")
	}
	var one int
	if err := rows.Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 returned %d", one)
	}
	fmt.Printf("SELECT 1 = %d\n", one)
	return rows.Err()
}
