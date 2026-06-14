package synthetic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"map-walker/internal/auth"
	"map-walker/internal/storage"
)

type userStore interface {
	LoadSyntheticUsers() ([]storage.SyntheticUserRecord, error)
	PrepareSyntheticUser(params storage.PrepareSyntheticUserParams) (storage.PrepareSyntheticUserResult, error)
	GetUserPosition(userID int64) (lat, lng float64, ok bool, err error)
}

type AccountReadiness struct {
	AccountNumber int
	UserID        int64
	Username      string
	Lat           float64
	Lng           float64
	Created       bool
	Reused        bool
	Corrected     bool
	Err           error
}

type ProvisionResult struct {
	Created   int
	Reused    int
	Corrected int
	Failed    int
	Accounts  []AccountReadiness
}

type Provisioner struct {
	Store            userStore
	Placement        PlacementConfig
	Now              func() time.Time
	ValidatePassword func(string) error
	HashPassword     func(string) (string, error)
	dbMu             sync.Mutex
}

func NewProvisioner(db *storage.DB) *Provisioner {
	return &Provisioner{
		Store:     db,
		Placement: DefaultPlacementConfig(),
		Now:       time.Now,
	}
}

func (p *Provisioner) Provision(ctx context.Context, count, workers int, password string) (ProvisionResult, error) {
	if count < 0 {
		return ProvisionResult{}, fmt.Errorf("count must be non-negative")
	}
	if workers < 1 {
		return ProvisionResult{}, fmt.Errorf("workers must be at least 1")
	}
	if count == 0 {
		return ProvisionResult{}, nil
	}

	existing, err := p.indexExisting()
	if err != nil {
		return ProvisionResult{}, err
	}

	accounts := make([]AccountReadiness, count)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for accountNumber := 1; accountNumber <= count; accountNumber++ {
		wg.Add(1)
		go func(accountNumber int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			accounts[accountNumber-1] = p.provisionAccount(ctx, accountNumber, password, existing[accountNumber])
		}(accountNumber)
	}
	wg.Wait()

	result := ProvisionResult{Accounts: accounts}
	for _, account := range accounts {
		switch {
		case account.Err != nil:
			result.Failed++
		case account.Created:
			result.Created++
		case account.Corrected:
			result.Corrected++
		case account.Reused:
			result.Reused++
		}
	}
	return result, nil
}

func (p *Provisioner) indexExisting() (map[int]storage.SyntheticUserRecord, error) {
	records, err := p.Store.LoadSyntheticUsers()
	if err != nil {
		return nil, err
	}

	indexed := map[int]storage.SyntheticUserRecord{}
	for _, record := range records {
		accountNumber, ok := ParseUsername(record.Username)
		if !ok {
			continue
		}
		indexed[accountNumber] = record
	}
	return indexed, nil
}

func (p *Provisioner) provisionAccount(
	ctx context.Context,
	accountNumber int,
	password string,
	existing storage.SyntheticUserRecord,
) AccountReadiness {
	readiness := AccountReadiness{
		AccountNumber: accountNumber,
		Username:      FormatUsername(accountNumber),
	}

	select {
	case <-ctx.Done():
		readiness.Err = ctx.Err()
		return readiness
	default:
	}

	lat, lng := PlacementLatLng(p.Placement, accountNumber)
	params := storage.PrepareSyntheticUserParams{
		Username:   readiness.Username,
		CreatedAt:  p.Now().UTC(),
		Appearance: FixedAppearance(),
		InitialLat: lat,
		InitialLng: lng,
	}

	hasExisting := existing.UserID != 0
	if !hasExisting {
		if err := p.validatePassword(password); err != nil {
			readiness.Err = err
			return readiness
		}
		hash, err := p.hashPassword(password)
		if err != nil {
			readiness.Err = err
			return readiness
		}
		params.PasswordHash = hash
	} else {
		params.ID = existing.UserID
	}

	result, err := p.prepareSyntheticUser(params)
	if err != nil {
		readiness.Err = err
		return readiness
	}

	readiness.UserID = result.UserID
	readiness.Lat, readiness.Lng = p.resolvePosition(existing, hasExisting, result, lat, lng)
	readiness.Created = result.Created
	switch {
	case result.Created:
	case result.AppearanceCorrected || result.PositionInitialized:
		readiness.Corrected = true
	default:
		readiness.Reused = true
	}
	return readiness
}

func (p *Provisioner) resolvePosition(
	existing storage.SyntheticUserRecord,
	hasExisting bool,
	result storage.PrepareSyntheticUserResult,
	initialLat, initialLng float64,
) (float64, float64) {
	if hasExisting && existing.HasPosition && !result.PositionInitialized {
		return existing.Lat, existing.Lng
	}

	lat, lng, ok, err := p.getUserPosition(result.UserID)
	if err == nil && ok {
		return lat, lng
	}
	return initialLat, initialLng
}

func (p *Provisioner) validatePassword(password string) error {
	if p.ValidatePassword != nil {
		return p.ValidatePassword(password)
	}
	return auth.ValidatePassword(password)
}

func (p *Provisioner) hashPassword(password string) (string, error) {
	if p.HashPassword != nil {
		return p.HashPassword(password)
	}
	return auth.HashPassword(password)
}

func (p *Provisioner) prepareSyntheticUser(params storage.PrepareSyntheticUserParams) (storage.PrepareSyntheticUserResult, error) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	return p.Store.PrepareSyntheticUser(params)
}

func (p *Provisioner) getUserPosition(userID int64) (lat, lng float64, ok bool, err error) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	return p.Store.GetUserPosition(userID)
}
