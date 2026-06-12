package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"
	"map-walker/internal/server"
	"map-walker/internal/storage"
)

func main() {
	host := flag.String("host", "0.0.0.0", "监听地址")
	port := flag.Int("port", 8080, "监听端口")
	dbDriver := flag.String("db-driver", "sqlite", "数据库驱动 (sqlite / mysql)")
	dbDSN := flag.String("db-dsn", storage.DefaultDBPath, "数据库 DSN (SQLite 文件路径 或 MySQL user:pass@tcp(host:port)/dbname)")
	flag.Parse()

	db, err := storage.Open(*dbDriver, *dbDSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	hub := realtime.NewHub()
	go hub.Run()

	srv := server.New(hub, auth.NewService(db))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("map-walker listening on http://%s", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
