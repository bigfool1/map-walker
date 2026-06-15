package storage

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"testing"
	"time"

	"map-walker/internal/realtime"
)

// benchContext 持有 benchmark 所需的 MySQL 连接和预创建的用户 ID。
type benchContext struct {
	db      *DB
	userIDs []int64
	size    int
}

func setupBenchMySQL(b *testing.B, size int) *benchContext {
	b.Helper()

	dsn := os.Getenv("MAP_WALKER_TEST_MYSQL_DSN")
	if dsn == "" {
		b.Skip("MAP_WALKER_TEST_MYSQL_DSN 未设置，跳过 MySQL 基准测试")
	}

	db, err := OpenMySQL(dsn)
	if err != nil {
		b.Fatalf("open mysql failed: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	// 用时间戳前缀确保每次 run 用户名唯一
	prefix := fmt.Sprintf("bench_%d_", time.Now().UnixNano())
	userIDs := make([]int64, size)
	for i := range userIDs {
		id, err := db.CreateUser(User{
			Username:           fmt.Sprintf("%s%d", prefix, i),
			UsernameNormalized: fmt.Sprintf("%s%d", prefix, i),
			PasswordHash:       "bench",
			CreatedAt:          time.Now().UTC(),
		})
		if err != nil {
			b.Fatalf("create bench user %d failed: %v", i, err)
		}
		userIDs[i] = id
	}

	return &benchContext{db: db, userIDs: userIDs, size: size}
}

// makeUpdates 生成 size 条位置更新，坐标伪随机但确定性（seed 固定）。
// makeUpdates 生成 size 条位置更新。每轮迭代坐标不同，避免 MySQL CLIENT_FOUND_ROWS
// 关闭时 RowsAffected=0 导致 baseline 误判 ErrNotFound。
func (c *benchContext) makeUpdates() []realtime.PositionUpdate {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	updates := make([]realtime.PositionUpdate, c.size)
	for i := range updates {
		updates[i] = realtime.PositionUpdate{
			UserID: c.userIDs[i],
			Lat:    rng.Float64()*90 - 45,
			Lng:    rng.Float64()*180 - 90,
		}
	}
	return updates
}

// BenchmarkPositionPersistenceBaseline 逐行 baseline：每个用户一次 UPDATE。
// 对应优化前的 SaveUserPosition 逐行写入策略。
func BenchmarkPositionPersistenceBaseline(b *testing.B) {
	for _, size := range []int{1000, 4000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			ctx := setupBenchMySQL(b, size)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				updates := ctx.makeUpdates()
				b.StartTimer()
				for _, u := range updates {
					if err := ctx.db.SaveUserPosition(u.UserID, u.Lat, u.Lng); err != nil {
						b.Fatalf("SaveUserPosition user=%d: %v", u.UserID, err)
					}
				}
			}

			b.ReportMetric(float64(size), "rows/submitted")
			b.ReportMetric(float64(size), "stmt/chunk")
		})
	}
}

// BenchmarkPositionPersistence 优化 chunk 批量：每 ≤500 行一个 UPDATE ... JOIN。
func BenchmarkPositionPersistence(b *testing.B) {
	for _, size := range []int{1000, 4000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			ctx := setupBenchMySQL(b, size)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				updates := ctx.makeUpdates()
				b.StartTimer()
				chunks := 0
				for start := 0; start < len(updates); start += MaxPositionChunkSize {
					end := min(start+MaxPositionChunkSize, len(updates))
					if err := ctx.db.SavePositionChunk(updates[start:end]); err != nil {
						b.Fatalf("SavePositionChunk offset=%d: %v", start, err)
					}
					chunks++
				}
				b.ReportMetric(float64(chunks), "chunks")
			}

			b.ReportMetric(float64(size), "rows/submitted")
		})
	}
}

// TestMySQLBenchmarkEnv 打印 benchmark 运行环境信息。
func TestMySQLBenchmarkEnv(t *testing.T) {
	dsn := os.Getenv("MAP_WALKER_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MAP_WALKER_TEST_MYSQL_DSN 未设置")
	}

	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatalf("open mysql failed: %v", err)
	}
	defer db.Close()

	var version string
	if err := db.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}

	t.Logf("MySQL version: %s", version)
	t.Logf("Go version:    %s", runtime.Version())
	t.Logf("GOARCH:        %s", runtime.GOARCH)
	t.Logf("GOOS:          %s", runtime.GOOS)
}
