package synthetic

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"map-walker/internal/auth"
	"map-walker/internal/storage"
)

const testPassword = "password123"

func openProvisionerTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestProvisioner(db *storage.DB) *Provisioner {
	return &Provisioner{
		Store:     db,
		Placement: DefaultPlacementConfig(),
		Now:       func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) },
	}
}

func TestProvisionerFillsGapsAndReusesExisting(t *testing.T) {
	db := openProvisionerTestDB(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	existingID, err := db.CreateUser(storage.User{
		Username:           "synthetic_2",
		UsernameNormalized: "synthetic_2",
		PasswordHash:       "existing-hash",
		CreatedAt:          now,
		Appearance:         FixedAppearance(),
	})
	if err != nil {
		t.Fatalf("create existing user failed: %v", err)
	}
	if err := db.SaveUserPosition(existingID, 31.1, 121.1); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	provisioner := newTestProvisioner(db)
	result, err := provisioner.Provision(context.Background(), 3, 2, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if result.Created != 2 || result.Reused != 1 || result.Failed != 0 {
		t.Fatalf("unexpected totals: %+v", result)
	}
	if len(result.Accounts) != 3 || result.Accounts[0].AccountNumber != 1 || result.Accounts[2].AccountNumber != 3 {
		t.Fatalf("unexpected account order: %+v", result.Accounts)
	}
	if !result.Accounts[1].Reused || result.Accounts[1].UserID != existingID {
		t.Fatalf("expected reused account 2, got %+v", result.Accounts[1])
	}
	if result.Accounts[1].Lat != 31.1 || result.Accounts[1].Lng != 121.1 {
		t.Fatalf("expected saved position preserved, got %+v", result.Accounts[1])
	}

	user, err := db.GetUserByID(existingID)
	if err != nil {
		t.Fatalf("get existing user failed: %v", err)
	}
	if user.PasswordHash != "existing-hash" {
		t.Fatalf("password hash changed to %q", user.PasswordHash)
	}
}

func TestProvisionerCorrectsAppearanceAndInitializesAbsentPosition(t *testing.T) {
	db := openProvisionerTestDB(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	id, err := db.CreateUser(storage.User{
		Username:           "synthetic_1",
		UsernameNormalized: "synthetic_1",
		PasswordHash:       "existing-hash",
		CreatedAt:          now,
		Appearance:         storage.Appearance{Color: "#3388ff", Shape: "circle"},
	})
	if err != nil {
		t.Fatalf("create existing user failed: %v", err)
	}

	result, err := newTestProvisioner(db).Provision(context.Background(), 1, 1, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if result.Corrected != 1 || result.Created != 0 {
		t.Fatalf("unexpected totals: %+v", result)
	}

	user, err := db.GetUserByID(id)
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.Appearance != FixedAppearance() {
		t.Fatalf("appearance not corrected: %+v", user.Appearance)
	}
	_, _, ok, err := db.GetUserPosition(id)
	if err != nil || !ok {
		t.Fatalf("expected absent position initialized, ok=%v err=%v", ok, err)
	}
}

func TestProvisionerRepeatExecutionIsIdempotent(t *testing.T) {
	db := openProvisionerTestDB(t)
	provisioner := newTestProvisioner(db)

	first, err := provisioner.Provision(context.Background(), 2, 2, testPassword)
	if err != nil {
		t.Fatalf("first provision failed: %v", err)
	}
	if first.Created != 2 {
		t.Fatalf("expected two created accounts, got %+v", first)
	}

	second, err := provisioner.Provision(context.Background(), 2, 2, testPassword)
	if err != nil {
		t.Fatalf("second provision failed: %v", err)
	}
	if second.Created != 0 || second.Corrected != 0 || second.Reused != 2 || second.Failed != 0 {
		t.Fatalf("expected full reuse, got %+v", second)
	}
}

func TestProvisionerShrinkTargetDoesNotDeleteHigherAccounts(t *testing.T) {
	db := openProvisionerTestDB(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	for _, name := range []string{"synthetic_1", "synthetic_2", "synthetic_5"} {
		_, err := db.CreateUser(storage.User{
			Username:           name,
			UsernameNormalized: name,
			PasswordHash:       "hash",
			CreatedAt:          now,
			Appearance:         FixedAppearance(),
		})
		if err != nil {
			t.Fatalf("create %s failed: %v", name, err)
		}
	}

	result, err := newTestProvisioner(db).Provision(context.Background(), 2, 1, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if len(result.Accounts) != 2 {
		t.Fatalf("expected two target accounts, got %d", len(result.Accounts))
	}
	if _, err := db.GetUserByNormalizedUsername("synthetic_5"); err != nil {
		t.Fatalf("expected higher-numbered account to remain: %v", err)
	}
}

func TestProvisionerIgnoresInvalidPrefixMatches(t *testing.T) {
	db := openProvisionerTestDB(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	_, err := db.CreateUser(storage.User{
		Username:           "synthetic_foo",
		UsernameNormalized: "synthetic_foo",
		PasswordHash:       "hash",
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("create invalid synthetic user failed: %v", err)
	}

	result, err := newTestProvisioner(db).Provision(context.Background(), 1, 1, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("expected canonical account creation, got %+v", result)
	}
}

func TestProvisionerContinuesAfterIndividualFailures(t *testing.T) {
	db := openProvisionerTestDB(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	// 预先创建 account 2（带错误外观，方便矫正路径触发失败）
	_, err := db.CreateUser(storage.User{
		Username:           "synthetic_2",
		UsernameNormalized: "synthetic_2",
		PasswordHash:       "existing-hash",
		CreatedAt:          now,
		Appearance:         storage.Appearance{Color: "#ffffff", Shape: "circle"},
	})
	if err != nil {
		t.Fatalf("create existing user failed: %v", err)
	}

	provisioner := newTestProvisioner(db)
	provisioner.Store = &faultInjectStore{
		db: db,
		failAccounts: map[int]error{
			2: errors.New("injected prepare failure"),
		},
	}

	result, err := provisioner.Provision(context.Background(), 3, 2, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if result.Failed != 1 || result.Created != 2 {
		t.Fatalf("unexpected totals: %+v", result)
	}
	if result.Accounts[1].Err == nil || result.Accounts[0].Err != nil || result.Accounts[2].Err != nil {
		t.Fatalf("expected only account 2 to fail: %+v", result.Accounts)
	}
}

func TestProvisionerWorkerConcurrencyStaysWithinBound(t *testing.T) {
	db := openProvisionerTestDB(t)
	provisioner := newTestProvisioner(db)

	var active int32
	var maxActive int32
	provisioner.HashPassword = func(password string) (string, error) {
		current := atomic.AddInt32(&active, 1)
		for {
			observed := atomic.LoadInt32(&maxActive)
			if current <= observed || atomic.CompareAndSwapInt32(&maxActive, observed, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return auth.HashPassword(password)
	}

	_, err := provisioner.Provision(context.Background(), 8, 2, testPassword)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if max := atomic.LoadInt32(&maxActive); max > 2 {
		t.Fatalf("worker concurrency exceeded bound: max=%d", max)
	}
}

func TestProvisionerRejectsMissingPasswordForCreates(t *testing.T) {
	db := openProvisionerTestDB(t)

	result, err := newTestProvisioner(db).Provision(context.Background(), 1, 1, "short")
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if result.Failed != 1 || !errors.Is(result.Accounts[0].Err, auth.ErrInvalidPassword) {
		t.Fatalf("expected invalid password failure, got %+v", result.Accounts[0])
	}
}

type faultInjectStore struct {
	db           *storage.DB
	failAccounts map[int]error
}

func (s *faultInjectStore) LoadSyntheticUsers() ([]storage.SyntheticUserRecord, error) {
	return s.db.LoadSyntheticUsers()
}

func (s *faultInjectStore) PrepareSyntheticUser(params storage.PrepareSyntheticUserParams) (storage.PrepareSyntheticUserResult, error) {
	if accountNumber, ok := ParseUsername(params.Username); ok {
		if err, failed := s.failAccounts[accountNumber]; failed {
			return storage.PrepareSyntheticUserResult{}, err
		}
	}
	return s.db.PrepareSyntheticUser(params)
}

func (s *faultInjectStore) BulkCreateSyntheticUsers(params []storage.BulkCreateSyntheticUserParams) (int, error) {
	for _, p := range params {
		if accountNumber, ok := ParseUsername(p.Username); ok {
			if err, failed := s.failAccounts[accountNumber]; failed {
				return 0, err
			}
		}
	}
	return s.db.BulkCreateSyntheticUsers(params)
}

func (s *faultInjectStore) GetUserPosition(userID int64) (float64, float64, bool, error) {
	return s.db.GetUserPosition(userID)
}
