package service

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// channelMonitorV3HTTPUpperBound returns a conservative price ceiling for the
// one bounded HTTP request that a V3 probe is allowed to send. The caller must
// pass an attempt that has already been Snapshot'ed: this function never reads
// account, channel, or pricing repositories.
//
// Price is deliberately re-used instead of duplicating its pricing rules. The
// candidates cover every reachable token-pricing interval boundary, cache
// class, and supported reasoning effort. A request with unknown or non-finite
// pricing therefore fails closed rather than spending an unreserved budget.
func channelMonitorV3HTTPUpperBound(
	ctx context.Context,
	calculator *UpstreamAttemptCostCalculator,
	attempt *UpstreamAttempt,
	maxInput, maxOutput int64,
) (float64, error) {
	if calculator == nil || calculator.billing == nil || attempt == nil ||
		!attempt.CostSnapshotAvailable || attempt.Model == "" || maxInput <= 0 || maxOutput <= 0 {
		return 0, fmt.Errorf("channel monitor v3 upstream pricing unavailable")
	}
	if maxInput > int64(^uint(0)>>1) || maxOutput > int64(^uint(0)>>1) ||
		maxInput > math.MaxInt64/3 || maxOutput > math.MaxInt64-maxInput*3 {
		return 0, fmt.Errorf("channel monitor v3 usage bound overflows pricing")
	}

	usages := channelMonitorV3BoundedUsageCandidates(attempt, maxInput, maxOutput)
	if len(usages) == 0 {
		return 0, fmt.Errorf("channel monitor v3 pricing bound unavailable")
	}
	efforts := channelMonitorV3BoundedReasoningEfforts(attempt)
	upper := 0.0
	for _, usage := range usages {
		if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheCreationTokens == 0 && usage.CacheReadTokens == 0 {
			continue
		}
		for _, effort := range efforts {
			priced := cloneChannelMonitorV3BoundAttempt(attempt)
			clearChannelMonitorV3BoundTimePricing(&priced)
			priced.UsagePresent = true
			priced.InputTokens = int64PtrForChannelMonitorV3(int64(usage.InputTokens))
			priced.OutputTokens = int64PtrForChannelMonitorV3(int64(usage.OutputTokens))
			priced.CacheCreationTokens = int64PtrForChannelMonitorV3(int64(usage.CacheCreationTokens))
			priced.CacheCreation5mTokens = int64PtrForChannelMonitorV3(int64(usage.CacheCreation5mTokens))
			priced.CacheCreation1hTokens = int64PtrForChannelMonitorV3(int64(usage.CacheCreation1hTokens))
			priced.CacheReadTokens = int64PtrForChannelMonitorV3(int64(usage.CacheReadTokens))
			priced.ReasoningEffort = effort
			if err := calculator.Price(ctx, &priced); err != nil {
				return 0, fmt.Errorf("channel monitor v3 upstream price cannot be bounded: %w", err)
			}
			if priced.CostUSD == nil || priced.CostStatus != "known" || math.IsNaN(derefFloat64ForChannelMonitorV3(priced.CostUSD)) || math.IsInf(derefFloat64ForChannelMonitorV3(priced.CostUSD), 0) || derefFloat64ForChannelMonitorV3(priced.CostUSD) < 0 {
				return 0, fmt.Errorf("channel monitor v3 upstream price cannot be bounded (status=%s input=%d output=%d write=%d read=%d effort=%s)", priced.CostStatus, usage.InputTokens, usage.OutputTokens, usage.CacheCreationTokens, usage.CacheReadTokens, effort)
			}
			upper = math.Max(upper, *priced.CostUSD)
		}
	}

	// Evaluate the untimed prices above, then apply the frozen schedule maximum.
	// This also bounds a request reserved during a discount period and sent later.
	upper *= channelMonitorV3MaximumTimePricingMultiplier(attempt)
	if math.IsNaN(upper) || math.IsInf(upper, 0) || upper < 0 {
		return 0, fmt.Errorf("channel monitor v3 upstream price is invalid")
	}
	return math.Ceil(upper*1e10) / 1e10, nil
}

func derefFloat64ForChannelMonitorV3(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func int64PtrForChannelMonitorV3(value int64) *int64 { return &value }

func cloneChannelMonitorV3BoundAttempt(attempt *UpstreamAttempt) UpstreamAttempt {
	cloned := *attempt
	cloned.pricingSnapshot = cloneResolvedPricing(attempt.pricingSnapshot)
	if attempt.accountStatsChannel != nil {
		cloned.accountStatsChannel = attempt.accountStatsChannel.Clone()
	}
	return cloned
}

func clearChannelMonitorV3BoundTimePricing(attempt *UpstreamAttempt) {
	if attempt.pricingSnapshot != nil && attempt.pricingSnapshot.channelPricing != nil {
		attempt.pricingSnapshot.channelPricing.TimePricing = nil
	}
	if attempt.accountStatsChannel == nil {
		return
	}
	for i := range attempt.accountStatsChannel.ModelPricing {
		attempt.accountStatsChannel.ModelPricing[i].TimePricing = nil
	}
	for i := range attempt.accountStatsChannel.AccountStatsPricingRules {
		for j := range attempt.accountStatsChannel.AccountStatsPricingRules[i].Pricing {
			attempt.accountStatsChannel.AccountStatsPricingRules[i].Pricing[j].TimePricing = nil
		}
	}
}

func channelMonitorV3BoundedReasoningEfforts(attempt *UpstreamAttempt) []string {
	efforts := map[string]struct{}{
		"none": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
	}
	if attempt != nil {
		efforts[attempt.ReasoningEffort] = struct{}{}
	}
	result := make([]string, 0, len(efforts))
	for effort := range efforts {
		result = append(result, effort)
	}
	sort.Strings(result)
	return result
}

func channelMonitorV3BoundedUsageCandidates(attempt *UpstreamAttempt, maxInput, maxOutput int64) []UsageTokens {
	maxContext := maxInput * 3 // input + one cache write + cache read
	maxTotal := maxContext + maxOutput
	contextTargets := channelMonitorV3PricingTargets(attempt, maxContext)
	totalTargets := channelMonitorV3PricingTargets(attempt, maxTotal)
	// A malformed or excessively granular admin price table must not turn one
	// monitor dispatch into unbounded CPU work. Failing closed preserves the
	// budget invariant; ordinary billing remains unaffected.
	if len(contextTargets) > 64 || len(totalTargets) > 64 {
		return nil
	}
	seen := make(map[UsageTokens]struct{})
	add := func(tokens UsageTokens) { seen[tokens] = struct{}{} }
	for _, target := range contextTargets {
		for _, tokens := range channelMonitorV3DistributeBoundedTokens(target, maxInput, maxOutput, false) {
			add(tokens)
		}
	}
	for _, target := range totalTargets {
		for _, tokens := range channelMonitorV3DistributeBoundedTokens(target, maxInput, maxOutput, true) {
			add(tokens)
		}
	}
	result := make([]UsageTokens, 0, len(seen))
	for tokens := range seen {
		result = append(result, tokens)
	}
	return result
}

// channelMonitorV3DistributeBoundedTokens enumerates vertices of the bounded
// token allocation polytope. Pricing is linear inside each selected tier, so a
// maximum for a tier occurs at one of these vertices. Each result is emitted
// twice for the 5m and 1h cache-write classes.
func channelMonitorV3DistributeBoundedTokens(target, maxInput, maxOutput int64, includeOutput bool) []UsageTokens {
	if target < 0 {
		return nil
	}
	const (
		inputSlot uint8 = iota
		creationSlot
		readSlot
		outputSlot
	)
	slots := []uint8{inputSlot, creationSlot, readSlot}
	capacity := map[uint8]int64{inputSlot: maxInput, creationSlot: maxInput, readSlot: maxInput, outputSlot: maxOutput}
	baseOutput := maxOutput
	if includeOutput {
		slots = append(slots, outputSlot)
		baseOutput = 0
	}
	maximum := int64(0)
	for _, item := range slots {
		maximum += capacity[item]
	}
	if target > maximum {
		target = maximum
	}
	permutations := channelMonitorV3SlotPermutations(slots)
	result := make([]UsageTokens, 0, len(permutations)*2)
	for _, ordering := range permutations {
		remaining := target
		allocated := map[uint8]int64{}
		for _, item := range ordering {
			allocated[item] = min(remaining, capacity[item])
			remaining -= allocated[item]
		}
		if remaining != 0 {
			continue
		}
		for _, oneHour := range []bool{false, true} {
			creation := allocated[creationSlot]
			tokens := UsageTokens{
				InputTokens:         int(allocated[inputSlot]),
				OutputTokens:        int(baseOutput + allocated[outputSlot]),
				CacheCreationTokens: int(creation),
				CacheReadTokens:     int(allocated[readSlot]),
			}
			if oneHour {
				tokens.CacheCreation1hTokens = int(creation)
			} else {
				tokens.CacheCreation5mTokens = int(creation)
			}
			result = append(result, tokens)
		}
	}
	return result
}

func channelMonitorV3SlotPermutations(values []uint8) [][]uint8 {
	if len(values) == 0 {
		return nil
	}
	result := make([][]uint8, 0)
	var walk func([]uint8, []uint8)
	walk = func(prefix, remaining []uint8) {
		if len(remaining) == 0 {
			result = append(result, append([]uint8(nil), prefix...))
			return
		}
		for i, item := range remaining {
			next := append([]uint8(nil), remaining[:i]...)
			next = append(next, remaining[i+1:]...)
			walk(append(prefix, item), next)
		}
	}
	walk(nil, values)
	return result
}

func channelMonitorV3PricingTargets(attempt *UpstreamAttempt, maximum int64) []int64 {
	targets := map[int64]struct{}{0: {}, maximum: {}}
	addIntervals := func(intervals []PricingInterval) {
		for _, interval := range intervals {
			for _, target := range []int64{int64(interval.MinTokens), int64(interval.MinTokens) + 1} {
				if target >= 0 && target <= maximum {
					targets[target] = struct{}{}
				}
			}
			if interval.MaxTokens != nil {
				target := int64(*interval.MaxTokens)
				if target >= 0 && target <= maximum {
					targets[target] = struct{}{}
				}
			}
		}
	}
	if attempt != nil && attempt.pricingSnapshot != nil {
		addIntervals(attempt.pricingSnapshot.Intervals)
		addIntervals(attempt.pricingSnapshot.RequestTiers)
		if pricing := attempt.pricingSnapshot.BasePricing; pricing != nil && pricing.LongContextInputThreshold > 0 {
			for _, target := range []int64{int64(pricing.LongContextInputThreshold), int64(pricing.LongContextInputThreshold) + 1} {
				if target <= maximum {
					targets[target] = struct{}{}
				}
			}
		}
	}
	if attempt != nil && attempt.accountStatsChannel != nil {
		for _, pricing := range attempt.accountStatsChannel.ModelPricing {
			addIntervals(pricing.Intervals)
		}
		for _, rule := range attempt.accountStatsChannel.AccountStatsPricingRules {
			for _, pricing := range rule.Pricing {
				addIntervals(pricing.Intervals)
			}
		}
	}
	result := make([]int64, 0, len(targets))
	for target := range targets {
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func channelMonitorV3MaximumTimePricingMultiplier(attempt *UpstreamAttempt) float64 {
	maximum := 1.0
	add := func(pricing ChannelModelPricing) {
		if pricing.TimePricing == nil {
			return
		}
		for _, period := range pricing.TimePricing.Periods {
			if period.Multiplier > maximum && !math.IsNaN(period.Multiplier) && !math.IsInf(period.Multiplier, 0) {
				maximum = period.Multiplier
			}
		}
	}
	if attempt == nil {
		return maximum
	}
	if attempt.pricingSnapshot != nil && attempt.pricingSnapshot.channelPricing != nil {
		add(*attempt.pricingSnapshot.channelPricing)
	}
	if attempt.accountStatsChannel != nil {
		for _, pricing := range attempt.accountStatsChannel.ModelPricing {
			add(pricing)
		}
		for _, rule := range attempt.accountStatsChannel.AccountStatsPricingRules {
			for _, pricing := range rule.Pricing {
				add(pricing)
			}
		}
	}
	return maximum
}
