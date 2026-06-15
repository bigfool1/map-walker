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
	BulkUpdateAppearances(userIDs []int64, appearance storage.Appearance) error
	BulkInitializePositions(entries []storage.BulkPositionEntry) error
	GetUserPosition(userID int64) (lat, lng float64, ok bool, err error)
	CorrectSyntheticMarkers() (int64, error)
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
	_ = ctx
	_ = workers
	if count < 0 {
		return ProvisionResult{}, fmt.Errorf("count must be non-negative")
	}
	if count == 0 {
		return ProvisionResult{}, nil
	}

	// 修正已有合成账户的 is_synthetic 标记（处理迁移前的旧数据）
	if _, err := p.Store.CorrectSyntheticMarkers(); err != nil {
		return ProvisionResult{}, fmt.Errorf("correct synthetic markers: %w", err)
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

	// 阶段 1：一次 hash + 批量创建
	if len(toCreate) > 0 {
		if err := p.validatePassword(password); err != nil {
			for _, n := range toCreate {
				accounts[n-1].Err = err
			}
			result := ProvisionResult{Accounts: accounts, Failed: len(toCreate)}
			return result, nil
		}

		passwordHash, err := p.hashPassword(password)
		if err != nil {
			for _, n := range toCreate {
				accounts[n-1].Err = err
			}
			result := ProvisionResult{Accounts: accounts, Failed: len(toCreate)}
			return result, nil
		}

		bulkParams := make([]storage.BulkCreateSyntheticUserParams, 0, len(toCreate))
		for _, accountNumber := range toCreate {
			lat, lng := PlacementLatLng(p.Placement, accountNumber)
			bulkParams = append(bulkParams, storage.BulkCreateSyntheticUserParams{
				Username:     FormatUsername(accountNumber),
				PasswordHash: passwordHash,
				CreatedAt:    p.Now().UTC(),
				Appearance:   FixedAppearance(),
				InitialLat:   lat,
				InitialLng:   lng,
			})
		}

		created, bulkErr := p.bulkCreateSyntheticUsers(bulkParams)
		for _, accountNumber := range toCreate {
			lat, lng := PlacementLatLng(p.Placement, accountNumber)
			idx := accountNumber - 1
			accounts[idx].Lat = lat
			accounts[idx].Lng = lng
			if bulkErr != nil {
				accounts[idx].Err = bulkErr
			} else {
				accounts[idx].Created = true
			}
		}
		_ = created
	}

	// 阶段 2：批量矫正已存在账户
	if len(toCorrect) > 0 {
		var needAppearance []int64
		var needPosition []storage.BulkPositionEntry

		for _, accountNumber := range toCorrect {
			ex := existing[accountNumber]
			lat, lng := PlacementLatLng(p.Placement, accountNumber)
			idx := accountNumber - 1
			accounts[idx].AccountNumber = accountNumber
			accounts[idx].Username = ex.Username
			accounts[idx].UserID = ex.UserID
			accounts[idx].Lat = ex.Lat
			accounts[idx].Lng = ex.Lng

			if ex.Appearance != FixedAppearance() {
				needAppearance = append(needAppearance, ex.UserID)
			}
			if !ex.HasPosition {
				needPosition = append(needPosition, storage.BulkPositionEntry{
					UserID: ex.UserID,
					Lat:    lat,
					Lng:    lng,
				})
				accounts[idx].Lat = lat
				accounts[idx].Lng = lng
			}

			if ex.Appearance == FixedAppearance() && ex.HasPosition {
				accounts[idx].Reused = true
			} else {
				accounts[idx].Corrected = true
			}
		}

		p.dbMu.Lock()
		if appErr := p.Store.BulkUpdateAppearances(needAppearance, FixedAppearance()); appErr != nil {
			p.dbMu.Unlock()
			failAccounts(accounts, toCorrect, appErr)
			return summarize(accounts), nil
		}
		if posErr := p.Store.BulkInitializePositions(needPosition); posErr != nil {
			p.dbMu.Unlock()
			failAccounts(accounts, toCorrect, posErr)
			return summarize(accounts), nil
		}
		p.dbMu.Unlock()
	}

	return summarize(accounts), nil
}

func failAccounts(accounts []AccountReadiness, target []int, err error) {
	for _, accountNumber := range target {
		accounts[accountNumber-1].Err = err
		accounts[accountNumber-1].Corrected = false
		accounts[accountNumber-1].Reused = false
	}
}

func summarize(accounts []AccountReadiness) ProvisionResult {
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
	return result
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

func (p *Provisioner) bulkCreateSyntheticUsers(params []storage.BulkCreateSyntheticUserParams) (int, error) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	return p.Store.BulkCreateSyntheticUsers(params)
}
