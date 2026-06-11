package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"map-walker/internal/realtime"
	"map-walker/internal/server"
)

func main() {
	host := flag.String("host", "0.0.0.0", "监听地址")
	port := flag.Int("port", 8080, "监听端口")
	flag.Parse()

	hub := realtime.NewHub()
	go hub.Run()

	srv := server.New(hub)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("map-walker listening on http://%s", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
