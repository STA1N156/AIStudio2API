package aistudio

import (
	"slices"
	"strings"
)

func (p *AccountPool) SetModelTiers(rules map[string][]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modelTiers = make(map[string][]string, len(rules))
	for model, tiers := range rules {
		p.modelTiers[model] = append([]string{}, tiers...)
	}
	p.notifyLocked()
}

func (p *AccountPool) allowedModelTiersLocked(modelID string) []string {
	if tiers, exists := p.modelTiers[modelID]; exists {
		return append([]string{}, tiers...)
	}
	return []string{"Ultra", "Pro", "Free"}
}

func (p *AccountPool) modelTierAllowedLocked(account *Account, modelID string) bool {
	modelID = canonicalAccountModelID(account, strings.TrimPrefix(strings.TrimSpace(modelID), "models/"))
	tiers, exists := p.modelTiers[modelID]
	return !exists || slices.Contains(tiers, account.BenefitTier.String())
}
