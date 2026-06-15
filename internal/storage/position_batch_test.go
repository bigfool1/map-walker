package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"map-walker/internal/realtime"
)

// ---- buildPositionChunkSQL 单元测试 ----

func TestBuildPositionChunkSQL_SingleRow(t *testing.T) {
	updates := []realtime.PositionUpdate{
		{UserID: 1, Lat: 31.1, Lng: 121.1},
	}

	query, args := buildPositionChunkSQL(updates)

	if !strings.Contains(query, "UPDATE users AS u JOIN (") {
		t.Fatalf("query 应包含 UPDATE users AS u JOIN: %s", query)
	}
	if strings.Contains(query, "UNION ALL") {
		t.Fatalf("单行查询不应包含 UNION ALL: %s", query)
	}
	if got := strings.Count(query, "?"); got != 3 {
		t.Fatalf("单行应有 3 个占位符，实际 %d: %s", got, query)
	}
	if len(args) != 3 {
		t.Fatalf("单行应有 3 个参数，实际 %d: %+v", len(args), args)
	}
	// 参数顺序：id, lat, lng
	if args[0].(int64) != 1 || args[1].(float64) != 31.1 || args[2].(float64) != 121.1 {
		t.Fatalf("参数顺序错误: %+v", args)
	}
}

func TestBuildPositionChunkSQL_MultipleRows(t *testing.T) {
	updates := []realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0},
		{UserID: 2, Lat: 31.0, Lng: 121.0},
		{UserID: 3, Lat: 32.0, Lng: 122.0},
	}

	query, args := buildPositionChunkSQL(updates)

	// 3 行应有 2 个 UNION ALL
	if got := strings.Count(query, "UNION ALL"); got != 2 {
		t.Fatalf("3 行应有 2 个 UNION ALL，实际 %d: %s", got, query)
	}
	// 3 行 × 3 列 = 9 个占位符
	if got := strings.Count(query, "?"); got != 9 {
		t.Fatalf("3 行应有 9 个占位符，实际 %d: %s", got, query)
	}
	if len(args) != 9 {
		t.Fatalf("3 行应有 9 个参数，实际 %d: %+v", len(args), args)
	}
	// 验证第二行参数位置
	if args[3].(int64) != 2 || args[4].(float64) != 31.0 || args[5].(float64) != 121.0 {
		t.Fatalf("第二行参数错误: %+v", args[3:6])
	}
}

func TestBuildPositionChunkSQL_MaxChunkSize(t *testing.T) {
	updates := make([]realtime.PositionUpdate, MaxPositionChunkSize)
	for i := range updates {
		updates[i] = realtime.PositionUpdate{
			UserID: int64(i + 1),
			Lat:    float64(i),
			Lng:    float64(i + 1),
		}
	}

	query, args := buildPositionChunkSQL(updates)

	expectedUnions := MaxPositionChunkSize - 1
	if got := strings.Count(query, "UNION ALL"); got != expectedUnions {
		t.Fatalf("%d 行应有 %d 个 UNION ALL，实际 %d", MaxPositionChunkSize, expectedUnions, got)
	}
	expectedPlaceholders := MaxPositionChunkSize * 3
	if got := strings.Count(query, "?"); got != expectedPlaceholders {
		t.Fatalf("%d 行应有 %d 个占位符，实际 %d", MaxPositionChunkSize, expectedPlaceholders, got)
	}
	if len(args) != expectedPlaceholders {
		t.Fatalf("%d 行应有 %d 个参数，实际 %d", MaxPositionChunkSize, expectedPlaceholders, len(args))
	}
}

// ---- SavePositionChunk 测试 ----

func TestSavePositionChunk_EmptyInput(t *testing.T) {
	db := openTestDB(t)

	if err := db.SavePositionChunk(nil); err != nil {
		t.Fatalf("nil updates 不应报错: %v", err)
	}
	if err := db.SavePositionChunk([]realtime.PositionUpdate{}); err != nil {
		t.Fatalf("空 updates 不应报错: %v", err)
	}
}

func TestSavePositionChunk_ExceedsMaxSize(t *testing.T) {
	db := openTestDB(t)
	updates := make([]realtime.PositionUpdate, MaxPositionChunkSize+1)

	err := db.SavePositionChunk(updates)
	if err == nil {
		t.Fatal("超过 MaxPositionChunkSize 应报错")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("错误信息应包含 'exceeds max': %v", err)
	}
}

func TestSavePositionChunk_BeginError(t *testing.T) {
	// 用已关闭的 DB 触发 Begin 失败
	db := openTestDB(t)
	db.Close()

	err := db.SavePositionChunk([]realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0},
	})
	if err == nil {
		t.Fatal("已关闭的 DB 应报错")
	}
	if !strings.Contains(err.Error(), "begin chunk transaction") {
		t.Fatalf("错误应包含 'begin chunk transaction': %v", err)
	}
}

func TestSavePositionChunk_ExecError(t *testing.T) {
	// 用 SQLite DB 执行 MySQL 语法会失败，覆盖 Exec 错误路径
	db := openTestDB(t)

	err := db.SavePositionChunk([]realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0},
	})
	if err == nil {
		t.Fatal("SQLite 上执行 MySQL UPDATE JOIN 语法应报错")
	}
	if !strings.Contains(err.Error(), "exec chunk update") {
		t.Fatalf("错误应包含 'exec chunk update': %v", err)
	}
}

// TestSavePositionChunk_Integration 真实 MySQL 集成测试。
// 设置 MAP_WALKER_TEST_MYSQL_DSN 环境变量后运行。
func TestSavePositionChunk_Integration(t *testing.T) {
	dsn := mysqlTestDSN(t)
	if dsn == "" {
		t.Skip("MAP_WALKER_TEST_MYSQL_DSN 未设置，跳过 MySQL 集成测试")
	}

	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatalf("open mysql failed: %v", err)
	}
	defer db.Close()

	// 创建测试用户（唯一用户名避免重复运行冲突）
	user1 := createTestUserForMySQL(t, db, "bulk")
	user2 := createTestUserForMySQL(t, db, "bulk")

	// 批量更新位置
	err = db.SavePositionChunk([]realtime.PositionUpdate{
		{UserID: user1, Lat: 31.1, Lng: 121.1},
		{UserID: user2, Lat: 31.2, Lng: 121.2},
	})
	if err != nil {
		t.Fatalf("bulk update failed: %v", err)
	}

	// 验证 user1
	lat, lng, ok, err := db.GetUserPosition(user1)
	if err != nil || !ok {
		t.Fatalf("user1 位置查询失败: ok=%v err=%v", ok, err)
	}
	if lat != 31.1 || lng != 121.1 {
		t.Fatalf("user1 位置错误: lat=%v lng=%v", lat, lng)
	}

	// 验证 user2
	lat, lng, ok, err = db.GetUserPosition(user2)
	if err != nil || !ok {
		t.Fatalf("user2 位置查询失败: ok=%v err=%v", ok, err)
	}
	if lat != 31.2 || lng != 121.2 {
		t.Fatalf("user2 位置错误: lat=%v lng=%v", lat, lng)
	}
}

// TestSavePositionChunk_Integration_NoRowsAffectedMisclassification
// 验证位置不变时不会误判为行缺失。
func TestSavePositionChunk_Integration_NoRowsAffectedMisclassification(t *testing.T) {
	dsn := mysqlTestDSN(t)
	if dsn == "" {
		t.Skip("MAP_WALKER_TEST_MYSQL_DSN 未设置，跳过 MySQL 集成测试")
	}

	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatalf("open mysql failed: %v", err)
	}
	defer db.Close()

	user1 := createTestUserForMySQL(t, db, "nochange")

	// 第一次：设置初始位置
	if err := db.SavePositionChunk([]realtime.PositionUpdate{
		{UserID: user1, Lat: 31.1, Lng: 121.1},
	}); err != nil {
		t.Fatalf("第一次更新失败: %v", err)
	}

	// 第二次：相同位置再次保存（应成功，不因 RowsAffected 报错）
	if err := db.SavePositionChunk([]realtime.PositionUpdate{
		{UserID: user1, Lat: 31.1, Lng: 121.1},
	}); err != nil {
		t.Fatalf("相同位置再次保存不应报错: %v", err)
	}
}

// createTestUserForMySQL 为 MySQL 集成测试创建唯一用户名用户。
func createTestUserForMySQL(t *testing.T, db *DB, suffix string) int64 {
	t.Helper()
	id, err := db.CreateUser(User{
		Username:           fmt.Sprintf("testuser_%s_%d", suffix, time.Now().UnixNano()),
		UsernameNormalized: fmt.Sprintf("testuser_%s_%d", suffix, time.Now().UnixNano()),
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	return id
}

func mysqlTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MAP_WALKER_TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = os.Getenv("MYSQL_DSN")
	}
	return dsn
}
