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
	BulkCreateSyntheticUsers(params []storage.BulkCreateSyntheticUserParams) (int, error)
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
	var toCreate []int
	var toCorrect []int

	for accountNumber := 1; accountNumber <= count; accountNumber++ {
		accounts[accountNumber-1] = AccountReadiness{
			AccountNumber: accountNumber,
			Username:      FormatUsername(accountNumber),
		}
		if _, ok := existing[accountNumber]; ok {
			toCorrect = append(toCorrect, accountNumber)
		} else {
			toCreate = append(toCreate, accountNumber)
		}
	}

	// 阶段 1：并发 hash 密码 + 批量创建
	if len(toCreate) > 0 {
		if err := p.validatePassword(password); err != nil {
			for _, n := range toCreate {
				accounts[n-1].Err = err
			}
			result := ProvisionResult{Accounts: accounts, Failed: len(toCreate)}
			return result, nil
		}

		type createEntry struct {
			accountNumber int
			name          string
			passwordHash  string
			lat, lng      float64
		}
		entries := make([]createEntry, len(toCreate))

		var hashWg sync.WaitGroup
		hashSem := make(chan struct{}, workers)
		for i, accountNumber := range toCreate {
			hashWg.Add(1)
			go func(idx, n int) {
				defer hashWg.Done()
				hashSem <- struct{}{}
				defer func() { <-hashSem }()
				hash, _ := p.hashPassword(password)
				lat, lng := PlacementLatLng(p.Placement, n)
				entries[idx] = createEntry{
					accountNumber: n,
					name:          FormatUsername(n),
					passwordHash:  hash,
					lat:           lat,
					lng:           lng,
				}
			}(i, accountNumber)
		}
		hashWg.Wait()

		bulkParams := make([]storage.BulkCreateSyntheticUserParams, 0, len(entries))
		for _, entry := range entries {
			bulkParams = append(bulkParams, storage.BulkCreateSyntheticUserParams{
				Username:     entry.name,
				PasswordHash: entry.passwordHash,
				CreatedAt:    p.Now().UTC(),
				Appearance:   FixedAppearance(),
				InitialLat:   entry.lat,
				InitialLng:   entry.lng,
			})
		}

		created, bulkErr := p.bulkCreateSyntheticUsers(bulkParams)
		for _, entry := range entries {
			idx := entry.accountNumber - 1
			accounts[idx].Lat = entry.lat
			accounts[idx].Lng = entry.lng
			if bulkErr != nil {
				accounts[idx].Err = bulkErr
			} else {
				accounts[idx].Created = true
			}
		}
		_ = created
	}

	// 阶段 2：矫正已存在账户（个数少，因无需 hash 密码故可直接并发）
	var corrWg sync.WaitGroup
	corrSem := make(chan struct{}, workers)
	for _, accountNumber := range toCorrect {
		corrWg.Add(1)
		go func(n int) {
			defer corrWg.Done()
			corrSem <- struct{}{}
			defer func() { <-corrSem }()
			accounts[n-1] = p.correctAccount(existing[n])
		}(accountNumber)
	}
	corrWg.Wait()

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

func (p *Provisioner) correctAccount(
	existing storage.SyntheticUserRecord,
) AccountReadiness {
	readiness := AccountReadiness{
		AccountNumber: 0,
		Username:      existing.Username,
	}

	params := storage.PrepareSyntheticUserParams{
		ID:         existing.UserID,
		Username:   existing.Username,
		CreatedAt:  p.Now().UTC(),
		Appearance: FixedAppearance(),
	}

	result, err := p.prepareSyntheticUser(params)
	if err != nil {
		readiness.Err = err
		return readiness
	}

	accountNumber, _ := ParseUsername(existing.Username)
	readiness.AccountNumber = accountNumber
	readiness.UserID = result.UserID
	readiness.Lat, readiness.Lng = p.resolveExistingPosition(existing, result)
	switch {
	case result.Created:
	case result.AppearanceCorrected || result.PositionInitialized:
		readiness.Corrected = true
	default:
		readiness.Reused = true
	}
	return readiness
}

func (p *Provisioner) resolveExistingPosition(
	existing storage.SyntheticUserRecord,
	result storage.PrepareSyntheticUserResult,
) (float64, float64) {
	if existing.HasPosition && !result.PositionInitialized {
		return existing.Lat, existing.Lng
	}
	lat, lng, ok, err := p.getUserPosition(result.UserID)
	if err == nil && ok {
		return lat, lng
	}
	return 0, 0
}

func (p *Provisioner) bulkCreateSyntheticUsers(params []storage.BulkCreateSyntheticUserParams) (int, error) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	return p.Store.BulkCreateSyntheticUsers(params)
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
