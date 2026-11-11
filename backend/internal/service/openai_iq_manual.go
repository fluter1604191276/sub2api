package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Recheck starts one manual run using the same admission and budget path as a
// scheduled run. confirmedLimit is the amount the administrator saw before
// confirming, so a concurrent settings change cannot silently increase it.
func (s *OpenAIIQService) Recheck(ctx context.Context, groupID, requestedBy int64, confirmedLimit float64) (int64, error) {
	if groupID <= 0 || requestedBy <= 0 || !finitePositive(confirmedLimit) {
		return 0, infraerrors.BadRequest("OPENAI_IQ_CONFIRM_INVALID", "A valid group and confirmed cost allowance are required")
	}
	// Register the entire admission, not just the worker, so shutdown also
	// waits for a claim that is in flight or being safely finalized.
	s.lifecycleMu.Lock()
	if s.stopped || s.ctx.Err() != nil {
		s.lifecycleMu.Unlock()
		return 0, infraerrors.ServiceUnavailable("OPENAI_IQ_STOPPED", "OpenAI IQ service is stopping")
	}
	s.manualWG.Add(1)
	s.lifecycleMu.Unlock()
	transferred := false
	defer func() {
		if !transferred {
			s.manualWG.Done()
		}
	}()
	ctx, cancelAdmission := context.WithCancel(ctx)
	stopAdmission := context.AfterFunc(s.ctx, cancelAdmission)
	defer func() { stopAdmission(); cancelAdmission() }()
	if !s.mu.TryLock() {
		return 0, iqManualClaimError(ErrIQRunActive)
	}
	release := true
	defer func() {
		if release {
			s.mu.Unlock()
		}
	}()
	if s.ctx.Err() != nil {
		return 0, infraerrors.ServiceUnavailable("OPENAI_IQ_STOPPED", "OpenAI IQ service is stopping")
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if settings.PerRequestLimitUSD > confirmedLimit {
		return 0, infraerrors.Conflict("OPENAI_IQ_COST_CHANGED", "The saved cost allowance increased; review and confirm it again")
	}
	if !slices.Contains(settings.GroupIDs, groupID) {
		return 0, iqManualClaimError(ErrIQGroupInvalid)
	}
	group, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			return 0, iqManualClaimError(ErrIQGroupInvalid)
		}
		return 0, err
	}
	if group == nil || group.Platform != PlatformOpenAI || group.Status != StatusActive {
		return 0, iqManualClaimError(ErrIQGroupInvalid)
	}
	identity, err := s.repo.Identity(ctx)
	if err != nil {
		return 0, err
	}
	if identity == nil || !finitePositive(identity.Balance) {
		return 0, infraerrors.Conflict("OPENAI_IQ_IDENTITY_UNAVAILABLE", "A funded, validated IQ test identity is required")
	}
	repo, ok := s.repo.(IQManualRunRepository)
	if !ok {
		return 0, infraerrors.ServiceUnavailable("OPENAI_IQ_MANUAL_UNAVAILABLE", "Manual OpenAI IQ runs are unavailable")
	}
	deadline := time.Now().Add(iqRoundTimeout)
	run, err := repo.ClaimManual(ctx, settings, groupID, requestedBy)
	if err != nil {
		return 0, iqManualClaimError(err)
	}
	if run == nil {
		return 0, infraerrors.ServiceUnavailable("OPENAI_IQ_MANUAL_UNAVAILABLE", "Manual OpenAI IQ run was not created")
	}
	run.Trigger = IQTriggerManual
	// If shutdown or request cancellation wins after the durable claim, finish
	// its pending result without dispatching traffic.
	s.lifecycleMu.Lock()
	if s.stopped || s.ctx.Err() != nil || ctx.Err() != nil {
		s.lifecycleMu.Unlock()
		finishCtx, cancel := context.WithTimeout(context.Background(), iqResultWriteTimeout)
		defer cancel()
		if err := s.repo.FinishRun(finishCtx, run.ID); err != nil {
			slog.Warn("OpenAI IQ cancelled manual run finalization failed")
		}
		return 0, infraerrors.ServiceUnavailable("OPENAI_IQ_STOPPED", "The manual run could not start")
	}
	transferred = true
	go func() {
		defer s.manualWG.Done()
		defer s.mu.Unlock()
		if err := s.executeIQRun(s.ctx, run, settings, identity.UserID, deadline); err != nil {
			// Provider responses and key-bearing errors must not be logged.
			slog.Warn("OpenAI IQ manual run could not complete")
		}
	}()
	release = false
	s.lifecycleMu.Unlock()
	return run.ID, nil
}

func iqManualClaimError(err error) error {
	switch {
	case errors.Is(err, ErrIQRunActive):
		return infraerrors.Conflict("OPENAI_IQ_RUN_ACTIVE", "An OpenAI IQ run is already active")
	case errors.Is(err, ErrIQManualCooldown):
		return infraerrors.TooManyRequests("OPENAI_IQ_MANUAL_COOLDOWN", "Wait five minutes before rechecking this group again")
	case errors.Is(err, ErrIQGroupInvalid):
		return infraerrors.BadRequest("OPENAI_IQ_GROUP_INVALID", "The group is not an active, configured OpenAI group")
	default:
		return err
	}
}
