//go:build localjournal

package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// LocalJournalGateway constructs the real services with no upstream, Redis,
// notification or account credentials. Available only under the local test tag.
func LocalJournalGateway(cfg *config.Config, billing UsageBillingRepository) *GatewayService {
	return NewGatewayService(nil, nil, nil, billing, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, &BillingCacheService{}, nil, nil, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func LocalJournalOpenAI(cfg *config.Config, billing UsageBillingRepository) *OpenAIGatewayService {
	s := NewOpenAIGatewayService(nil, nil, billing, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, &BillingCacheService{}, nil, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	s.userGroupRateResolver = newUserGroupRateResolver(nil, nil, resolveUserGroupRateCacheTTL(cfg), nil, "local.synthetic")
	return s
}

// Test-only constructor; the provided HTTP transport is an in-memory fixture.
// No credential store, token refresher or real HTTP client is attached.
func LocalJournalAntigravity(cfg *config.Config, upstream HTTPUpstream) *AntigravityGatewayService {
	return &AntigravityGatewayService{settingService: &SettingService{cfg: cfg}, tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
}

// Loads the repository's existing pricing resource via the real pricing parser.
// No Initialize, polling or remote client is used by this local-only fixture.
func LocalJournalGatewayWithPricingFile(cfg *config.Config, billing UsageBillingRepository, path string) (*GatewayService, error) {
	p := NewPricingService(cfg, nil)
	if e := p.loadPricingData(path); e != nil {
		return nil, e
	}
	s := LocalJournalGateway(cfg, billing)
	s.billingService = NewBillingService(cfg, p)
	return s, nil
}
