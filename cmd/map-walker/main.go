package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	worker := storage.NewPersistenceWorker(db)
	hub := realtime.NewHubWithSavedPositions(storage.SavedPositionLoader(db), worker)
	go hub.Run()

	srv := server.New(hub, auth.NewService(db))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Routes(),
	}

	go func() {
		log.Printf("map-walker listening on http://%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}

	hub.Stop()

	if err := db.Close(); err != nil {
		log.Printf("database close error: %v", err)
	}
	log.Println("shutdown complete")
}
