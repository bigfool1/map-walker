package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"map-walker/internal/realtime"
	"map-walker/internal/server"
	"map-walker/internal/storage"
)

func main() {
	host := flag.String("host", "0.0.0.0", "监听地址")
	port := flag.Int("port", 8080, "监听端口")
	dbPath := flag.String("db", storage.DefaultDBPath, "SQLite 数据库路径")
	flag.Parse()

	db, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	hub := realtime.NewHub()
	go hub.Run()

	srv := server.New(hub)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("map-walker listening on http://%s", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
