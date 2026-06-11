package main

import (
	"log"
	"net/http"

	"map-walker/internal/realtime"
	"map-walker/internal/server"
)

func main() {
	hub := realtime.NewHub()
	go hub.Run()

	srv := server.New(hub)

	addr := ":8080"
	log.Printf("map-walker listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
