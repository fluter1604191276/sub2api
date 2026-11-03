package service

import (
	"strings"
	"sync"
	"time"
)

const (
	// openAIModelTransientStreakTTL bounds how long a failure streak survives
	// without a new failure. It exists only so the map does not keep state for
	// account+model pairs that stopped being used; a streak is otherwise reset
	// by recordSuccess alone.
	//
	// It must stay well above the cooldowns. Resetting the streak on a short
	// wall-clock window makes the breaker's sensitivity depend on request rate:
	// a gateway called less often than the window never reaches streak 2, so a
	// broken upstream is never cooled down and every request pays a failed
	// attempt plus a failover before reaching a healthy account. Low-traffic
	// deployments were hit hardest, which is the opposite of what a breaker
	// should do.
	openAIModelTransientStreakTTL     = 30 * time.Minute
	openAIModelTransientShortCooldown = 10 * time.Second
	openAIModelTransientLongCooldown  = 45 * time.Second
	openAIModelTransientDefaultMax    = 4096
	openAIModelTransientMaxModelBytes = 512
	// Empty Responses completions use the same account+normalized-model key as
	// the generic transient breaker, with their own window. Two real empty
	// results inside five minutes isolate that pair for thirty minutes. A later
	// short success does not erase the isolation.
	openAIEmptyStreamWindow    = 5 * time.Minute
	openAIEmptyStreamThreshold = 2
	openAIEmptyStreamIsolation = 30 * time.Minute
	openAIEmptyStreamDedupeCap = 32
)

type openAIAccountModelKey struct {
	AccountID int64
	Model     string
}

type openAIAccountModelTransientEntry struct {
	failureStreak int
	lastFailure   time.Time
	blockUntil    time.Time
	lastTouched   time.Time

	emptyCount         int
	emptyWindowStart   time.Time
	emptyRequestIDs    map[string]struct{}
	emptyIsolatedUntil time.Time
}

type openAIAccountModelTransientDecision struct {
	FailureStreak int
	Cooldown      time.Duration
	BlockUntil    time.Time
}

type openAIAccountModelTransientState struct {
	mu         sync.Mutex
	entries    map[openAIAccountModelKey]openAIAccountModelTransientEntry
	maxEntries int
}

func newOpenAIAccountModelTransientState(maxEntries int) *openAIAccountModelTransientState {
	if maxEntries <= 0 {
		maxEntries = openAIModelTransientDefaultMax
	}
	return &openAIAccountModelTransientState{
		entries:    make(map[openAIAccountModelKey]openAIAccountModelTransientEntry),
		maxEntries: maxEntries,
	}
}

func normalizeOpenAIAccountModelTransientModel(model string) string {
	model = strings.TrimSpace(model)
	if len(model) > openAIModelTransientMaxModelBytes {
		return ""
	}
	return strings.ToLower(model)
}

func openAIAccountModelTransientKey(accountID int64, model string) (openAIAccountModelKey, bool) {
	model = normalizeOpenAIAccountModelTransientModel(model)
	if accountID <= 0 || model == "" {
		return openAIAccountModelKey{}, false
	}
	return openAIAccountModelKey{AccountID: accountID, Model: model}, true
}

func (s *openAIAccountModelTransientState) recordFailure(accountID int64, model string, now time.Time) openAIAccountModelTransientDecision {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return openAIAccountModelTransientDecision{}
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[openAIAccountModelKey]openAIAccountModelTransientEntry)
	}
	if s.maxEntries <= 0 {
		s.maxEntries = openAIModelTransientDefaultMax
	}

	entry, exists := s.entries[key]
	if !exists {
		s.evictOldestLocked()
	}
	// The streak is cleared by recordSuccess. Only drop it here when the entry
	// is stale beyond the TTL, or when the clock moved backwards.
	if !exists || entry.lastFailure.IsZero() || now.Sub(entry.lastFailure) > openAIModelTransientStreakTTL || now.Before(entry.lastFailure) {
		entry.failureStreak = 0
		entry.blockUntil = time.Time{}
	}
	entry.failureStreak++
	entry.lastFailure = now
	entry.lastTouched = now

	cooldown := time.Duration(0)
	switch {
	case entry.failureStreak >= 3:
		cooldown = openAIModelTransientLongCooldown
	case entry.failureStreak == 2:
		cooldown = openAIModelTransientShortCooldown
	}
	if cooldown > 0 {
		entry.blockUntil = now.Add(cooldown)
	} else {
		entry.blockUntil = time.Time{}
	}
	s.entries[key] = entry
	return openAIAccountModelTransientDecision{
		FailureStreak: entry.failureStreak,
		Cooldown:      cooldown,
		BlockUntil:    entry.blockUntil,
	}
}

func (s *openAIAccountModelTransientState) forceBlock(accountID int64, model string, now time.Time, cooldown time.Duration) openAIAccountModelTransientDecision {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok || cooldown <= 0 {
		return openAIAccountModelTransientDecision{}
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[openAIAccountModelKey]openAIAccountModelTransientEntry)
	}
	if s.maxEntries <= 0 {
		s.maxEntries = openAIModelTransientDefaultMax
	}
	entry, exists := s.entries[key]
	if !exists {
		s.evictOldestLocked()
	}
	entry.failureStreak++
	entry.lastFailure = now
	entry.lastTouched = now
	forcedUntil := now.Add(cooldown)
	if entry.blockUntil.Before(forcedUntil) {
		entry.blockUntil = forcedUntil
	}
	s.entries[key] = entry
	return openAIAccountModelTransientDecision{
		FailureStreak: entry.failureStreak,
		Cooldown:      entry.blockUntil.Sub(now),
		BlockUntil:    entry.blockUntil,
	}
}

func (s *openAIAccountModelTransientState) recordSuccess(accountID int64, model string) {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return
	}
	// A short success, including a hi probe, must not erase empty-stream
	// evidence. Only the generic transient streak is cleared here.
	entry.failureStreak = 0
	entry.blockUntil = time.Time{}
	entry.lastFailure = time.Time{}
	if !entry.hasEmptyStreamState() {
		delete(s.entries, key)
		return
	}
	entry.lastTouched = time.Now()
	s.entries[key] = entry
}

func (entry openAIAccountModelTransientEntry) hasEmptyStreamState() bool {
	return entry.emptyCount > 0 || !entry.emptyIsolatedUntil.IsZero()
}

// recordEmptyStream counts one real empty or abnormally terminated Responses
// result. The same request ID is counted once. The second distinct result
// inside the window isolates the account+model pair.
func (s *openAIAccountModelTransientState) recordEmptyStream(accountID int64, model, requestID string, now time.Time) openAIAccountModelTransientDecision {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return openAIAccountModelTransientDecision{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	requestID = strings.TrimSpace(requestID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[openAIAccountModelKey]openAIAccountModelTransientEntry)
	}
	if s.maxEntries <= 0 {
		s.maxEntries = openAIModelTransientDefaultMax
	}
	entry, exists := s.entries[key]
	if !exists {
		s.evictOldestLocked()
	}
	if entry.emptyWindowStart.IsZero() || now.Before(entry.emptyWindowStart) || now.Sub(entry.emptyWindowStart) > openAIEmptyStreamWindow {
		entry.emptyCount = 0
		entry.emptyWindowStart = now
		entry.emptyRequestIDs = nil
	}
	if requestID != "" {
		if entry.emptyRequestIDs == nil {
			entry.emptyRequestIDs = make(map[string]struct{}, 2)
		}
		if _, seen := entry.emptyRequestIDs[requestID]; seen {
			entry.lastTouched = now
			s.entries[key] = entry
			return emptyStreamDecision(entry, now)
		}
		if len(entry.emptyRequestIDs) >= openAIEmptyStreamDedupeCap {
			entry.emptyRequestIDs = map[string]struct{}{requestID: {}}
		}
		entry.emptyRequestIDs[requestID] = struct{}{}
	}
	entry.emptyCount++
	entry.lastTouched = now
	if entry.emptyCount >= openAIEmptyStreamThreshold {
		isolatedUntil := now.Add(openAIEmptyStreamIsolation)
		if entry.emptyIsolatedUntil.Before(isolatedUntil) {
			entry.emptyIsolatedUntil = isolatedUntil
		}
	}
	s.entries[key] = entry
	return emptyStreamDecision(entry, now)
}

func (s *openAIAccountModelTransientState) isEmptyStreamIsolated(accountID int64, model string, now time.Time) bool {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists || entry.emptyIsolatedUntil.IsZero() {
		return false
	}
	return now.Before(entry.emptyIsolatedUntil)
}

func emptyStreamDecision(entry openAIAccountModelTransientEntry, now time.Time) openAIAccountModelTransientDecision {
	decision := openAIAccountModelTransientDecision{FailureStreak: entry.emptyCount}
	if !entry.emptyIsolatedUntil.IsZero() && now.Before(entry.emptyIsolatedUntil) {
		decision.BlockUntil = entry.emptyIsolatedUntil
		decision.Cooldown = entry.emptyIsolatedUntil.Sub(now)
	}
	return decision
}

func (s *openAIAccountModelTransientState) isBlocked(accountID int64, model string, now time.Time) bool {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return false
	}
	if !entry.emptyIsolatedUntil.IsZero() && now.Before(entry.emptyIsolatedUntil) {
		entry.lastTouched = now
		s.entries[key] = entry
		return true
	}
	if !entry.emptyIsolatedUntil.IsZero() && !now.Before(entry.emptyIsolatedUntil) {
		entry.emptyIsolatedUntil = time.Time{}
		entry.emptyCount = 0
		entry.emptyWindowStart = time.Time{}
		entry.emptyRequestIDs = nil
	}
	if !entry.hasEmptyStreamState() && entry.failureStreak == 0 && entry.blockUntil.IsZero() && entry.lastFailure.IsZero() {
		delete(s.entries, key)
		return false
	}
	if !entry.blockUntil.IsZero() && now.Before(entry.blockUntil) {
		entry.lastTouched = now
		s.entries[key] = entry
		return true
	}
	if !entry.lastFailure.IsZero() && now.Sub(entry.lastFailure) > openAIModelTransientStreakTTL {
		delete(s.entries, key)
		return false
	}
	entry.lastTouched = now
	s.entries[key] = entry
	return false
}

func (s *openAIAccountModelTransientState) blockInfo(accountID int64, model string, now time.Time) (time.Duration, int, bool) {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return 0, 0, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return 0, 0, false
	}
	if !entry.emptyIsolatedUntil.IsZero() && now.Before(entry.emptyIsolatedUntil) {
		streak := entry.emptyCount
		if entry.failureStreak > streak {
			streak = entry.failureStreak
		}
		return entry.emptyIsolatedUntil.Sub(now), streak, true
	}
	if entry.blockUntil.IsZero() || !now.Before(entry.blockUntil) {
		return 0, 0, false
	}
	return entry.blockUntil.Sub(now), entry.failureStreak, true
}

func (s *openAIAccountModelTransientState) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *openAIAccountModelTransientState) evictOldestLocked() {
	if len(s.entries) < s.maxEntries {
		return
	}
	var oldestKey openAIAccountModelKey
	var oldestTime time.Time
	found := false
	for key, entry := range s.entries {
		if !found || entry.lastTouched.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastTouched
			found = true
		}
	}
	if found {
		delete(s.entries, oldestKey)
	}
}
