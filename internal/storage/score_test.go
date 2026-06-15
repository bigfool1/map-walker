package storage

import (
	"testing"
	"time"
)

func createUserForScoreTest(t *testing.T, db *DB, username string) int64 {
	t.Helper()
	id, err := db.CreateUser(User{
		Username:           username,
		UsernameNormalized: username,
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return id
}

func TestSaveScoreMonotonic(t *testing.T) {
	db := openTestDB(t)
	userID := createUserForScoreTest(t, db, "score_user")

	// 保存初始分数
	if err := db.SaveScore(userID, 42); err != nil {
		t.Fatalf("SaveScore(42) 失败: %v", err)
	}
	user, err := db.GetUserByID(userID)
	if err != nil {
		t.Fatalf("GetUserByID 失败: %v", err)
	}
	if user.CollectibleScore != 42 {
		t.Fatalf("分数 = %d, want 42", user.CollectibleScore)
	}

	// 保存更低分数，不应减少
	if err := db.SaveScore(userID, 10); err != nil {
		t.Fatalf("SaveScore(10) 失败: %v", err)
	}
	user, _ = db.GetUserByID(userID)
	if user.CollectibleScore != 42 {
		t.Fatalf("保存更低分数后 = %d, want 42", user.CollectibleScore)
	}

	// 保存更高分数，应增加
	if err := db.SaveScore(userID, 100); err != nil {
		t.Fatalf("SaveScore(100) 失败: %v", err)
	}
	user, _ = db.GetUserByID(userID)
	if user.CollectibleScore != 100 {
		t.Fatalf("保存更高分数后 = %d, want 100", user.CollectibleScore)
	}

	// 重复保存相同分数，幂等
	if err := db.SaveScore(userID, 100); err != nil {
		t.Fatalf("重复 SaveScore(100) 失败: %v", err)
	}
	user, _ = db.GetUserByID(userID)
	if user.CollectibleScore != 100 {
		t.Fatalf("重复保存后 = %d, want 100", user.CollectibleScore)
	}
}

func TestSaveScoreZeroAffectedRows(t *testing.T) {
	db := openTestDB(t)
	userID := createUserForScoreTest(t, db, "zero_rows_user")

	// 先设为 50
	db.SaveScore(userID, 50)
	// 再尝试设为 30（应被 MONOTONIC 忽略，不影响行数）
	err := db.SaveScore(userID, 30)
	if err != nil {
		t.Fatalf("SaveScore(30) 应在 RowsAffected==0 时返回 nil: %v", err)
	}
}

func TestNewUserHasZeroScore(t *testing.T) {
	db := openTestDB(t)
	userID := createUserForScoreTest(t, db, "fresh_user")

	user, err := db.GetUserByID(userID)
	if err != nil {
		t.Fatalf("GetUserByID 失败: %v", err)
	}
	if user.CollectibleScore != 0 {
		t.Fatalf("新用户分数 = %d, want 0", user.CollectibleScore)
	}
	if user.IsSynthetic {
		t.Fatal("新用户不应是合成账户")
	}
}
