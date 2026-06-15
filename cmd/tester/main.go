package main

import (
	"flag"
	"log"
	"os"

	"github.com/joho/godotenv"

	"map-walker/internal/storage"
	"map-walker/internal/tester"
)

func main() {
	// .env 文件存在则加载，不存在则忽略
	_ = godotenv.Load()

	target := flag.Int("target", 0, "合成客户端数量（必填，>0）")
	rampRate := flag.Int("ramp-rate", 10, "每秒激活速率")
	host := flag.String("host", "localhost", "服务器地址")
	port := flag.String("port", "8080", "服务器端口")

	defaultDriver := envDefault("DB_DRIVER", "sqlite")
	defaultDSN := envDefault("DB_DSN", storage.DefaultDBPath)
	dbDriver := flag.String("db-driver", defaultDriver, "数据库驱动 (sqlite / mysql)")
	dbDSN := flag.String("db-dsn", defaultDSN, "数据库 DSN")

	password := flag.String("password", os.Getenv("MAP_WALKER_SYNTHETIC_PASSWORD"), "合成账户密码（用于自动创建账户）")
	flag.Parse()

	if *target <= 0 {
		log.Fatalf("-target 必须 > 0，当前值: %d", *target)
	}

	db, err := storage.Open(*dbDriver, *dbDSN)
	if err != nil {
		log.Fatalf("打开数据库: %v", err)
	}
	defer db.Close()

	cfg := tester.Config{
		TargetCount: *target,
		RampRate:    *rampRate,
		Host:        *host,
		Port:        *port,
		Password:    *password,
	}

	tester.NewManager(cfg, db).Run()
}

// envDefault 返回环境变量的值，若未设置则返回 fallback。
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
