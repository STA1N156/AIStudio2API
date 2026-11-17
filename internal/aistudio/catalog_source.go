package aistudio

import "time"

func usableCatalogAccount(account *Account) bool {
	return account != nil && account.Config.Enabled &&
		(account.State == AccountReady || account.State == AccountBusy || account.State == AccountCooldown)
}

// CatalogAccountIDs 优先沿用目录来源，再尝试有空闲槽位的其他账户。
func (s *PooledService) CatalogAccountIDs() []string {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	ids := make([]string, 0, len(s.pool.accounts))
	available := func(account *Account) bool {
		return usableCatalogAccount(account) && !account.exclusive && account.exclusiveWaiters == 0 &&
			account.authRefreshers == 0 && account.active < s.pool.perAccountConcurrency
	}
	if account := s.pool.byID[s.pool.catalogAccountID]; available(account) {
		ids = append(ids, account.ID)
	}
	for _, account := range s.pool.accounts {
		if available(account) && account.ID != s.pool.catalogAccountID {
			ids = append(ids, account.ID)
		}
	}
	return ids
}

// UseCatalogAccount 只切换公共目录来源，不改账户资格、冷却或启停状态。
func (s *PooledService) UseCatalogAccount(accountID string) {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	if account := s.pool.byID[accountID]; usableCatalogAccount(account) && len(account.Models) > 0 {
		s.pool.catalogAccountID = accountID
	}
}

func (s *PooledService) CatalogFresh(maxAge time.Duration, hidden bool) bool {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	account := s.pool.byID[s.pool.catalogAccountID]
	return usableCatalogAccount(account) && len(account.Models) > 0 &&
		time.Since(account.catalogSyncedAt) < maxAge && account.hiddenDiscovered == hidden &&
		(hidden || len(account.hiddenModels) == 0)
}

// cachedCatalogSource 只复制一个来源的元数据，后续处理不占用选号锁。
func (s *PooledService) cachedCatalogSource() *AccountPool {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	account := s.pool.byID[s.pool.catalogAccountID]
	if !usableCatalogAccount(account) || len(account.Models) == 0 {
		account = nil
		for _, candidate := range s.pool.accounts {
			if usableCatalogAccount(candidate) && len(candidate.Models) > 0 {
				account = candidate
				break
			}
		}
	}
	if account == nil {
		return nil
	}
	s.pool.catalogAccountID = account.ID
	return &AccountPool{
		accounts: []*Account{{ID: account.ID, Config: account.Config, BenefitTier: account.BenefitTier,
			Models: account.Models, buildModels: account.buildModels}},
		channels: s.pool.channels, modelTiers: s.pool.modelTiers,
	}
}
