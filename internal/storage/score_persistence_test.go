package storage

import (
	"sync"
	"testing"
	"time"
)

func setupPersister(t *testing.T) (*DB, *ScorePersister, func()) {
	t.Helper()
	db := openTestDB(t)
	persister := NewScorePersister(db)
	cleanup := func() {
		persister.Stop()
	}
	return db, persister, cleanup
}

func createScoreTestUser(t *testing.T, db *DB, username string) int64 {
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

func assertScore(t *testing.T, db *DB, userID int64, want int64) {
	t.Helper()
	user, err := db.GetUserByID(userID)
	if err != nil {
		t.Fatalf("GetUserByID(%d) 失败: %v", userID, err)
	}
	if user.CollectibleScore != want {
		t.Fatalf("用户 %d 分数 = %d, want %d", userID, user.CollectibleScore, want)
	}
}

func TestScorePersisterSubmitAndDrain(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player1")
	persister.Submit(ScoreUpdate{UserID: userID, Score: 42})
	persister.Drain()

	assertScore(t, db, userID, 42)
}

func TestScorePersisterCoalescing(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player2")
	// 快速连续提交递增分数，应只持久化最高值
	persister.Submit(ScoreUpdate{UserID: userID, Score: 41})
	persister.Submit(ScoreUpdate{UserID: userID, Score: 42})
	persister.Submit(ScoreUpdate{UserID: userID, Score: 43})
	persister.Drain()

	assertScore(t, db, userID, 43)
}

func TestScorePersisterSubmitSync(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player3")
	persister.SubmitSync(userID, 99)

	assertScore(t, db, userID, 99)
}

func TestScorePersisterSyncTakesPendingMax(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player4")
	// 异步提交更高分数，然后同步提交较低分数，应取 pending 中的最高值
	persister.Submit(ScoreUpdate{UserID: userID, Score: 77})
	time.Sleep(50 * time.Millisecond) // 让 worker 合并
	persister.SubmitSync(userID, 10)  // Sync 应取 pending 中的 77

	assertScore(t, db, userID, 77)
}

func TestScorePersisterPerUserIsolation(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	alice := createScoreTestUser(t, db, "alice")
	bob := createScoreTestUser(t, db, "bob")

	persister.Submit(ScoreUpdate{UserID: alice, Score: 10})
	persister.Submit(ScoreUpdate{UserID: bob, Score: 20})
	persister.Drain()

	assertScore(t, db, alice, 10)
	assertScore(t, db, bob, 20)
}

func TestScorePersisterMultipleDrainIdempotent(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player5")
	persister.Submit(ScoreUpdate{UserID: userID, Score: 55})
	persister.Drain()
	persister.Drain() // 第二次 drain 应无影响

	assertScore(t, db, userID, 55)
}

func TestScorePersisterStaleSnapshot(t *testing.T) {
	// 验证：旧快照持久化成功后不会清除已被更新的 pending 条目
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player6")

	// 提交 42 并等待 worker 处理
	persister.Submit(ScoreUpdate{UserID: userID, Score: 42})
	persister.Drain()
	assertScore(t, db, userID, 42)

	// 提交 44
	persister.Submit(ScoreUpdate{UserID: userID, Score: 44})
	persister.Drain()
	assertScore(t, db, userID, 44)
}

func TestScorePersisterConcurrentSubmissions(t *testing.T) {
	db, persister, cleanup := setupPersister(t)
	defer cleanup()

	userID := createScoreTestUser(t, db, "player7")

	var wg sync.WaitGroup
	for score := int64(1); score <= 10; score++ {
		wg.Add(1)
		go func(s int64) {
			defer wg.Done()
			persister.Submit(ScoreUpdate{UserID: userID, Score: s})
		}(score)
	}
	wg.Wait()
	persister.Drain()

	// 最终分数应 >= 1（至少持久化了其中一个）
	assertScore(t, db, userID, 10) // 10 goroutines, worker should persist max
}
