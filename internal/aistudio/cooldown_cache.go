package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"
)

// File-backed accounts may also be changed by another process without a pool notification.
const externalCooldownRecheck = 30 * time.Second

type coolingScope struct {
	Model, Access, Method, Capability, Account string
	Channel, Preferred                         Channel
	PlaygroundOnly                             bool
	Allowed                                    [sha256.Size]byte
}

type coolingDecision struct {
	cooldown CooldownState
	recheck  time.Time
}

func coolingScopeOf(s AccountSelection) (coolingScope, bool) {
	model := strings.TrimPrefix(strings.TrimSpace(s.ModelID), "models/")
	if model == "" || s.ResourceID != "" || s.IgnoreQuotaCooldown || s.anyState || s.anyTier {
		return coolingScope{}, false
	}
	key := coolingScope{Model: model, Access: selectionAccessScope(s), Method: s.Method,
		Capability: s.Capability, Account: s.AccountID, Channel: s.Channel, Preferred: s.PreferredChannel,
		PlaygroundOnly: s.PlaygroundOnly}
	if s.AllowedAccountIDs != nil {
		ids := slices.Clone(s.AllowedAccountIDs)
		slices.Sort(ids)
		data, _ := json.Marshal(slices.Compact(ids))
		key.Allowed = sha256.Sum256(data)
	}
	return key, true
}

func (g AccountCandidateGroups) allCooling() bool {
	return g.Eligible && !g.EarliestCooldown.Until.IsZero() &&
		len(g.WarmReady)+len(g.WarmAvailable)+len(g.WarmBusy)+len(g.StandbyReady)+len(g.StandbyBusy) == 0
}

func (p *AccountPool) cachedCoolingLocked(key coolingScope) (CooldownState, bool) {
	v, found := p.confirmedCooling[key]
	if !found {
		return CooldownState{}, false
	}
	now := time.Now()
	if !now.Before(v.recheck) || !now.Before(v.cooldown.Until) {
		delete(p.confirmedCooling, key)
		return CooldownState{}, false
	}
	return v.cooldown, true
}

// ConfirmedCooldown only reports a verified, all-cooling scope, never a busy or empty pool.
func (p *AccountPool) ConfirmedCooldown(ctx context.Context, s AccountSelection) (CooldownState, bool) {
	p.mu.Lock()
	empty := len(p.confirmedCooling) == 0
	p.mu.Unlock()
	if empty {
		return CooldownState{}, false
	}
	key, ok := coolingScopeOf(p.scopedSelection(ctx, s))
	if !ok {
		return CooldownState{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cachedCoolingLocked(key)
}

func (p *AccountPool) confirmCooling(ctx context.Context, s AccountSelection, key coolingScope) error {
	encoded, _ := json.Marshal(key)
	result := p.cooldownChecks.DoChan(string(encoded), func() (any, error) {
		p.mu.Lock()
		_, found := p.cachedCoolingLocked(key)
		p.mu.Unlock()
		if found {
			return nil, nil
		}
		// A disconnected queue head must not cancel the refresh for the other waiters.
		refresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), externalCooldownRecheck)
		defer cancel()
		if err := p.refreshSelectionRuntimes(refresh, s); err != nil {
			return nil, err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		g, err := p.classifyCandidatesLocked(s, nil)
		if err == nil && g.allCooling() {
			if p.confirmedCooling == nil {
				p.confirmedCooling = make(map[coolingScope]coolingDecision)
			}
			if len(p.confirmedCooling) >= 256 {
				clear(p.confirmedCooling)
			}
			p.confirmedCooling[key] = coolingDecision{cooldown: g.EarliestCooldown, recheck: time.Now().Add(externalCooldownRecheck)}
		}
		return nil, err
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case v := <-result:
		return v.Err
	}
}

func (p *AccountPool) invalidateRuntimeCoolingLocked(old, next accountRuntimeState) {
	if len(p.confirmedCooling) == 0 {
		return
	}
	if old.Restricted != next.Restricted || old.CatalogFingerprint != next.CatalogFingerprint || old.BenefitTier != next.BenefitTier ||
		!maps.EqualFunc(old.ModelAccess, next.ModelAccess, func(a, b ModelAccess) bool { return a.State == b.State }) {
		clear(p.confirmedCooling)
		return
	}
	changed := func(scope string) bool {
		a, b := old.Cooldowns[scope], next.Cooldowns[scope]
		return !a.Until.Equal(b.Until) || a.Reason != b.Reason
	}
	if changed(globalCooldownKey) {
		clear(p.confirmedCooling)
		return
	}
	for k := range p.confirmedCooling {
		if changed(k.Access) || changed(ChannelCooldownScope(ChannelBuild, k.Access)) {
			delete(p.confirmedCooling, k)
		}
	}
}
