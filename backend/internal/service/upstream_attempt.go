package service

import (
	"context"
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// UpstreamAttempt records one raw upstream HTTP call. It is an internal shadow
// ledger and must never be used to charge a user or affect account selection.
type UpstreamAttempt struct {
	ID                        int64            `json:"id"`
	AttemptID                 string           `json:"attempt_id"`
	RequestID                 string           `json:"request_id"`
	ClientRequestID           string           `json:"client_request_id"`
	UpstreamRequestID         string           `json:"upstream_request_id"`
	CreatedAt                 time.Time        `json:"created_at"`
	UserID                    *int64           `json:"user_id,omitempty"`
	GroupID                   *int64           `json:"group_id,omitempty"`
	AccountID                 int64            `json:"account_id"`
	Model                     string           `json:"model"`
	Protocol                  string           `json:"protocol"`
	Source                    string           `json:"source"`
	ServiceTier               string           `json:"service_tier,omitempty"`
	ReasoningEffort           string           `json:"reasoning_effort,omitempty"`
	UpstreamRequestIDHeader   string           `json:"-"`
	LongContextPricingEnabled bool             `json:"long_context_pricing_enabled"`
	HTTPStatus                int              `json:"http_status"`
	Outcome                   string           `json:"outcome"`
	UsagePresent              bool             `json:"usage_present"`
	InputTokens               *int64           `json:"input_tokens,omitempty"`
	CacheCreationTokens       *int64           `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens           *int64           `json:"cache_read_tokens,omitempty"`
	CacheCreation5mTokens     *int64           `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens     *int64           `json:"cache_creation_1h_tokens,omitempty"`
	OutputTokens              *int64           `json:"output_tokens,omitempty"`
	ReasoningTokens           *int64           `json:"reasoning_tokens,omitempty"`
	AccountRateMultiplier     float64          `json:"account_rate_multiplier"`
	CostSnapshotAvailable     bool             `json:"-"`
	pricingSnapshot           *ResolvedPricing `json:"-"`
	accountStatsChannel       *Channel         `json:"-"`
	accountStatsPlatform      string           `json:"-"`
	CostUSD                   *float64         `json:"cost_usd,omitempty"`
	CostStatus                string           `json:"cost_status"`
}

// Admin list paging bounds shared by the diagnostics and upstream-attempt
// endpoints and their repositories, so (page-1)*size can never overflow.
const (
	AdminListMaxPage     = 10000
	AdminListMaxPageSize = 1000
)

type UpstreamAttemptFilter struct {
	StartTime, EndTime *time.Time
	AccountID          *int64
	Source             string
	Page, PageSize     int
}
type UpstreamAttemptList struct {
	Items    []*UpstreamAttempt `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}
type UpstreamAttemptDailyCost struct {
	Day          time.Time `json:"day"`
	AccountID    int64     `json:"account_id"`
	AttemptCount int64     `json:"attempt_count"`
	KnownCostUSD float64   `json:"known_cost_usd"`
	UnknownCount int64     `json:"unknown_count"`
}
type UpstreamAttemptCostSummary struct {
	KnownCostUSD float64 `json:"known_cost_usd"`
	UnknownCount int64   `json:"unknown_count"`
	AttemptCount int64   `json:"attempt_count"`
}
type UpstreamAttemptRepository interface {
	Insert(context.Context, *UpstreamAttempt) error
	List(context.Context, *UpstreamAttemptFilter) (*UpstreamAttemptList, error)
	DailyCosts(context.Context, *int64, time.Time, time.Time) ([]UpstreamAttemptDailyCost, error)
	SumByClientRequestID(context.Context, string, string) (UpstreamAttemptCostSummary, error)
}

func ValidUpstreamAttemptSource(value string) bool {
	switch value {
	case "request", "recovery_probe", "channel_monitor", "channel_monitor_v3", "openai_iq":
		return true
	}
	return false
}
func ValidUpstreamAttemptOutcome(value string) bool {
	switch value {
	case "success", "failed", "incomplete", "unknown":
		return true
	}
	return false
}

func NormalizeUpstreamAttempt(attempt *UpstreamAttempt) error {
	if attempt == nil {
		return fmt.Errorf("nil upstream attempt")
	}
	for _, value := range []*string{&attempt.AttemptID, &attempt.RequestID, &attempt.ClientRequestID, &attempt.UpstreamRequestID, &attempt.Model, &attempt.Protocol, &attempt.Source, &attempt.ServiceTier, &attempt.ReasoningEffort, &attempt.Outcome, &attempt.CostStatus} {
		*value = strings.TrimSpace(*value)
	}
	if attempt.AttemptID == "" || !safeUpstreamAttemptIdentifier(attempt.AttemptID, 128) || attempt.AccountID <= 0 || attempt.Protocol == "" || !safeUpstreamAttemptIdentifier(attempt.Protocol, 64) {
		return fmt.Errorf("invalid upstream attempt identity")
	}
	// Request-derived metadata is diagnostic only. Drop unsupported values rather
	// than losing a no-usage failure row or persisting free-form request content.
	for _, value := range []*string{&attempt.Model, &attempt.ServiceTier, &attempt.ReasoningEffort} {
		if *value != "" && !safeUpstreamAttemptIdentifier(*value, 256) {
			*value = ""
		}
	}
	for _, value := range []*string{&attempt.RequestID, &attempt.ClientRequestID, &attempt.UpstreamRequestID} {
		if *value != "" && !safeUpstreamAttemptIdentifier(*value, 256) {
			*value = ""
		}
	}
	if !ValidUpstreamAttemptSource(attempt.Source) || !ValidUpstreamAttemptOutcome(attempt.Outcome) || attempt.HTTPStatus < 0 || attempt.HTTPStatus > 999 {
		return fmt.Errorf("invalid upstream attempt metadata")
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC()
	}
	if attempt.UserID != nil && *attempt.UserID <= 0 {
		attempt.UserID = nil
	}
	if attempt.GroupID != nil && *attempt.GroupID <= 0 {
		attempt.GroupID = nil
	}
	for _, value := range []**int64{&attempt.InputTokens, &attempt.CacheCreationTokens, &attempt.CacheReadTokens, &attempt.CacheCreation5mTokens, &attempt.CacheCreation1hTokens, &attempt.OutputTokens, &attempt.ReasoningTokens} {
		if *value != nil && **value < 0 {
			return fmt.Errorf("negative upstream attempt token count")
		}
	}
	if !attempt.UsagePresent {
		attempt.CostUSD = nil
		attempt.CostStatus = "no_usage"
	} else if attempt.CostUSD == nil {
		attempt.CostStatus = "unknown_pricing"
	} else if math.IsNaN(*attempt.CostUSD) || math.IsInf(*attempt.CostUSD, 0) || *attempt.CostUSD < 0 {
		return fmt.Errorf("invalid upstream attempt cost")
	} else {
		attempt.CostStatus = "known"
	}
	if math.IsNaN(attempt.AccountRateMultiplier) || math.IsInf(attempt.AccountRateMultiplier, 0) || attempt.AccountRateMultiplier < 0 {
		return fmt.Errorf("invalid upstream account multiplier")
	}
	return nil
}

func safeUpstreamAttemptIdentifier(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.:/", r)) {
			return false
		}
	}
	return true
}

// UpstreamAttemptCostCalculator snapshots account pricing before dispatch, then
// prices only the observed token fields. It has no billing-side effects.
type UpstreamAttemptCostCalculator struct {
	accounts AccountRepository
	channels *ChannelService
	billing  *BillingService
}

func NewUpstreamAttemptCostCalculator(accounts AccountRepository, channels *ChannelService, billing *BillingService) *UpstreamAttemptCostCalculator {
	return &UpstreamAttemptCostCalculator{accounts: accounts, channels: channels, billing: billing}
}
func (c *UpstreamAttemptCostCalculator) Snapshot(ctx context.Context, attempt *UpstreamAttempt) error {
	if attempt == nil || attempt.AccountID <= 0 {
		return fmt.Errorf("invalid upstream attempt")
	}
	attempt.CostSnapshotAvailable = false
	if c == nil || c.accounts == nil {
		return nil
	}
	account, err := c.accounts.GetByID(ctx, attempt.AccountID)
	if err != nil || account == nil {
		// Leave the snapshot unavailable: Price must represent a missing snapshot
		// as unknown rather than treating the zero-value multiplier as free.
		return err
	}
	attempt.AccountRateMultiplier = account.BillingRateMultiplier()
	attempt.LongContextPricingEnabled = accountStatsLongContextPricingEnabled(openAILongContextBillingGate(account))
	attempt.UpstreamRequestIDHeader = UpstreamRequestIDHeaderName(account)
	if c.billing == nil || attempt.Model == "" {
		return nil
	}
	var groupID *int64
	if attempt.GroupID != nil && *attempt.GroupID > 0 {
		id := *attempt.GroupID
		groupID = &id
	}
	resolver := NewModelPricingResolver(nil, c.billing)
	pricingInput := PricingInput{Model: attempt.Model}
	var directChannelPricing *ChannelModelPricing
	if c.channels != nil && groupID != nil {
		if channel, channelErr := c.channels.GetChannelForGroup(ctx, *groupID); channelErr == nil && channel != nil {
			attempt.accountStatsChannel = channel.Clone()
			attempt.accountStatsPlatform = c.channels.GetGroupPlatform(ctx, *groupID)
			if channel.ApplyPricingToAccountStats {
				resolver = NewModelPricingResolver(c.channels, c.billing)
				pricingInput.GroupID = groupID
				if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && group.ID == *groupID {
					pricingInput.Group = group
				}
				if pricingInput.Group == nil {
					directChannelPricing = channel.GetModelPricing(attempt.Model)
				}
			}
		}
	}
	if directChannelPricing != nil {
		attempt.pricingSnapshot = cloneResolvedPricing(NewModelPricingResolver(nil, c.billing).resolveConfiguredPricing(directChannelPricing, attempt.Model, PricingSourceChannel))
	} else {
		attempt.pricingSnapshot = cloneResolvedPricing(resolver.Resolve(ctx, pricingInput))
	}
	if attempt.pricingSnapshot != nil {
		attempt.pricingSnapshot.longContextPricingEnabled = attempt.LongContextPricingEnabled
	}
	attempt.CostSnapshotAvailable = true
	return nil
}

func (c *UpstreamAttemptCostCalculator) Price(ctx context.Context, attempt *UpstreamAttempt) error {
	if err := NormalizeUpstreamAttempt(attempt); err != nil {
		return err
	}
	if !attempt.UsagePresent {
		return nil
	}
	if !upstreamAttemptHasTokenUsage(attempt) {
		attempt.CostUSD = nil
		attempt.CostStatus = "unknown_pricing"
		return nil
	}
	if c == nil || c.billing == nil || !attempt.CostSnapshotAvailable || attempt.Model == "" {
		attempt.CostUSD = nil
		attempt.CostStatus = "unknown_pricing"
		return nil
	}
	tokens := UsageTokens{InputTokens: int64ToInt(attempt.InputTokens), CacheCreationTokens: int64ToInt(attempt.CacheCreationTokens), CacheReadTokens: int64ToInt(attempt.CacheReadTokens), CacheCreation5mTokens: int64ToInt(attempt.CacheCreation5mTokens), CacheCreation1hTokens: int64ToInt(attempt.CacheCreation1hTokens), OutputTokens: int64ToInt(attempt.OutputTokens)}
	groupID := int64(0)
	if attempt.GroupID != nil {
		groupID = *attempt.GroupID
	}
	var base *float64
	if attempt.accountStatsChannel != nil {
		base = tryCustomRules(attempt.accountStatsChannel, attempt.AccountID, groupID, attempt.accountStatsPlatform, attempt.Model, tokens, 1, attempt.CreatedAt, attempt.ReasoningEffort)
	}
	if base == nil {
		if attempt.pricingSnapshot == nil {
			attempt.CostUSD = nil
			attempt.CostStatus = "unknown_pricing"
			return nil
		}
		resolver := NewModelPricingResolver(nil, c.billing)
		breakdown, err := c.billing.CalculateCostUnified(CostInput{
			Ctx:             ctx,
			Model:           attempt.Model,
			Tokens:          tokens,
			RequestCount:    1,
			RateMultiplier:  1,
			PricingAt:       attempt.CreatedAt,
			ServiceTier:     normalizeBillingServiceTier(attempt.ServiceTier),
			ReasoningEffort: attempt.ReasoningEffort,
			Resolver:        resolver,
			Resolved:        attempt.pricingSnapshot,
		})
		if err == nil && breakdown != nil && breakdown.TotalCost >= 0 && !math.IsNaN(breakdown.TotalCost) && !math.IsInf(breakdown.TotalCost, 0) {
			base = &breakdown.TotalCost
		}
	}
	if base == nil {
		attempt.CostUSD = nil
		attempt.CostStatus = "unknown_pricing"
		return nil
	}
	cost := *base * attempt.AccountRateMultiplier
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		attempt.CostUSD = nil
		attempt.CostStatus = "unknown_pricing"
		return nil
	}
	attempt.CostUSD = &cost
	attempt.CostStatus = "known"
	return nil
}

func cloneResolvedPricing(pricing *ResolvedPricing) *ResolvedPricing {
	if pricing == nil {
		return nil
	}
	cloned := *pricing
	cloned.BasePricing = cloneModelPricing(pricing.BasePricing)
	if cloned.BasePricing != nil {
		cloned.BasePricing.ReasoningEffortMultipliers = maps.Clone(pricing.BasePricing.ReasoningEffortMultipliers)
	}
	cloned.Intervals = append([]PricingInterval(nil), pricing.Intervals...)
	cloned.RequestTiers = append([]PricingInterval(nil), pricing.RequestTiers...)
	if pricing.channelPricing != nil {
		item := pricing.channelPricing.Clone()
		cloned.channelPricing = &item
	}
	return &cloned
}

func upstreamAttemptHasTokenUsage(attempt *UpstreamAttempt) bool {
	return attempt.InputTokens != nil ||
		attempt.CacheCreationTokens != nil ||
		attempt.CacheReadTokens != nil ||
		attempt.CacheCreation5mTokens != nil ||
		attempt.CacheCreation1hTokens != nil ||
		attempt.OutputTokens != nil ||
		attempt.ReasoningTokens != nil
}

func int64ToInt(value *int64) int {
	if value == nil || *value > int64(^uint(0)>>1) {
		return 0
	}
	return int(*value)
}
