//go:build unit

package service

import "time"

// Test adapters retain the recommended-policy contract; production selects its
// explicit policy through the existing WithPolicy methods.
func (s *OpenAIGatewayService) applySmartStickyReviewState(key string, now time.Time, decision openAISmartStickyReviewDecision) openAISmartStickyReviewDecision {
	return s.applySmartStickyReviewStateWithPolicy(key, now, decision, RecommendedSmartStickyPolicy())
}

func (s *OpenAIGatewayService) markSmartStickySwitchApplied(key string, accountID int64, now time.Time, scores ...*float64) {
	s.markSmartStickySwitchAppliedWithPolicy(key, accountID, now, RecommendedSmartStickyPolicy(), scores...)
}

func smartStickyCooldownCanBeBroken(decision openAISmartStickyReviewDecision, remaining time.Duration) bool {
	return smartStickyCooldownCanBeBrokenWithPolicy(decision, remaining, RecommendedSmartStickyPolicy())
}

func decideOpenAISmartStickyReview(ordering *SmartSchedulerOrdering, currentAccountID int64) openAISmartStickyReviewDecision {
	return decideOpenAISmartStickyReviewWithPolicy(ordering, currentAccountID, RecommendedSmartStickyPolicy())
}
