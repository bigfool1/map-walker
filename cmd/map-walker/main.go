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

	"github.com/joho/godotenv"

	"map-walker/internal/auth"
	"map-walker/internal/game"
	"map-walker/internal/realtime"
	"map-walker/internal/server"
	"map-walker/internal/storage"
	"map-walker/internal/synthetic"
)

type syntheticFlags struct {
	count         int
	rampRate      int
	autoProvision bool
}

func main() {
	// .env 文件存在则加载，不存在则忽略
	_ = godotenv.Load()

	host := flag.String("host", "0.0.0.0", "监听地址")
	port := flag.Int("port", 8080, "监听端口")

	defaultDriver := envDefault("DB_DRIVER", "sqlite")
	defaultDSN := envDefault("DB_DSN", storage.DefaultDBPath)
	dbDriver := flag.String("db-driver", defaultDriver, "数据库驱动 (sqlite / mysql)")
	dbDSN := flag.String("db-dsn", defaultDSN, "数据库 DSN (SQLite 文件路径 或 MySQL user:pass@tcp(host:port)/dbname)")

	synFlags := syntheticFlags{}
	flag.IntVar(&synFlags.count, "synthetic-clients", 0, "合成客户端数量 (0 = 禁用)")
	flag.IntVar(&synFlags.rampRate, "synthetic-ramp-rate", 10, "合成客户端每秒激活速率 (0 = 无限制)")
	flag.BoolVar(&synFlags.autoProvision, "synthetic-auto-provision", false, "自动创建缺失的合成账号")
	flag.Parse()

	if err := validateSyntheticFlags(synFlags); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	db, err := storage.Open(*dbDriver, *dbDSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	worker := storage.NewPersistenceWorker(db)
	loadSavedPlayer := storage.SavedPlayerLoader(db)
	hub := realtime.NewHubWithSavedPositions(func(userID int64) (realtime.SavedPlayerLoad, bool) {
		state, ok := loadSavedPlayer(userID)
		if !ok {
			return realtime.SavedPlayerLoad{}, false
		}
		return realtime.SavedPlayerLoad{
			Username:    state.Username,
			Lat:         state.Lat,
			Lng:         state.Lng,
			HasPosition: state.HasPosition,
			Appearance: game.Appearance{
				Color: state.Appearance.Color,
				Shape: state.Appearance.Shape,
			},
		}, true
	}, worker)
	go hub.Run()

	var manager *synthetic.Manager
	if synFlags.count > 0 {
		manager, err = buildSyntheticManager(hub, db, synFlags)
		if err != nil {
			log.Fatalf("build synthetic manager: %v", err)
		}
		manager.Start(context.Background())
	}

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

	if manager != nil {
		manager.Stop()
	}

	hub.Stop()

	if err := db.Close(); err != nil {
		log.Printf("database close error: %v", err)
	}
	log.Println("shutdown complete")
}

func buildSyntheticManager(hub *realtime.Hub, db *storage.DB, flags syntheticFlags) (*synthetic.Manager, error) {
	password := ""
	if flags.autoProvision {
		password = os.Getenv("MAP_WALKER_SYNTHETIC_PASSWORD")
	}

	cfg := synthetic.ManagerConfig{
		TargetCount:   flags.count,
		RampRate:      flags.rampRate,
		AutoProvision: flags.autoProvision,
		Password:      password,
		Behavior:      synthetic.DefaultBehaviorConfig(),
	}

	deps := synthetic.ManagerDeps{
		Hub:         hub,
		Store:       db,
		Provisioner: synthetic.NewProvisioner(db),
	}

	return synthetic.NewManager(cfg, deps)
}

func validateSyntheticFlags(flags syntheticFlags) error {
	if flags.count < 0 {
		return fmt.Errorf("synthetic-clients must be non-negative, got %d", flags.count)
	}
	if flags.rampRate < 0 {
		return fmt.Errorf("synthetic-ramp-rate must be non-negative, got %d", flags.rampRate)
	}
	return nil
}

// envDefault 返回环境变量的值，若未设置则返回 fallback。
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
