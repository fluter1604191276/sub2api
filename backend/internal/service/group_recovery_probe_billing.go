package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const SettingKeyGroupRecoveryProbeBilling = "group_recovery_probe_billing"

const (
	groupRecoveryProbeDefaultDailyBudgetUSD     = 1.0
	groupRecoveryProbeDefaultPerAttemptLimitUSD = 0.01
	groupRecoveryProbeMaxDailyBudgetUSD         = 1000.0
)

var ErrGroupRecoveryProbeBudgetExceeded = infraerrors.TooManyRequests(
	"GROUP_RECOVERY_PROBE_BUDGET_EXCEEDED",
	"智能探针今日预算已用尽，已暂停后续探针",
)

type GroupRecoveryProbeBillingSettings struct {
	Enabled            bool    `json:"enabled"`
	OwnerUserID        int64   `json:"owner_user_id"`
	APIKeyID           int64   `json:"api_key_id"`
	APIKeyName         string  `json:"api_key_name,omitempty"`
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`
	PerAttemptLimitUSD float64 `json:"per_attempt_limit_usd"`
}

type GroupRecoveryProbeBillingSummary struct {
	TodaySettledCost float64 `json:"today_settled_cost"`
	TodayBudgetCost  float64 `json:"today_budget_cost"`
	TodayAttempts    int64   `json:"today_attempts"`
	TodaySettled     int64   `json:"today_settled"`
	TodayUnavailable int64   `json:"today_unavailable"`
	TodayFailed      int64   `json:"today_failed"`
}

type GroupRecoveryProbeBillingStatus struct {
	Settings     GroupRecoveryProbeBillingSettings `json:"settings"`
	GlobalToday  GroupRecoveryProbeBillingSummary  `json:"global_today"`
	GroupToday   GroupRecoveryProbeBillingSummary  `json:"group_today"`
	RemainingUSD float64                           `json:"remaining_usd"`
}

type GroupRecoveryProbeAuditSettlement struct {
	Status          string
	SettledCost     *float64
	UsageLogID      *int64
	BillingUserID   *int64
	BillingAPIKeyID *int64
	Error           string
	CostStatus      string
}

type GroupRecoveryProbeBillingRepository interface {
	GetBillingSummary(ctx context.Context, groupID int64, since time.Time) (GroupRecoveryProbeBillingSummary, error)
	UpdateAuditSettlement(ctx context.Context, auditID int64, settlement GroupRecoveryProbeAuditSettlement) error
}

type GroupRecoveryProbeAtomicSettlementCommand struct {
	AuditID        int64
	ReservationUSD float64
	DailyBudgetUSD float64
	BudgetSince    time.Time
	SettledCostUSD float64
	BillingCommand *UsageBillingCommand
	UsageLog       *UsageLog
}

type GroupRecoveryProbeAtomicSettlementResult struct {
	Status         string
	BillingApplied bool
	UsageLogID     *int64
}

type GroupRecoveryProbeAtomicSettlementRepository interface {
	SettleProbe(ctx context.Context, command *GroupRecoveryProbeAtomicSettlementCommand) (*GroupRecoveryProbeAtomicSettlementResult, error)
}

type groupRecoveryProbeAPIKeyReader interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
}

type groupRecoveryProbeAccountReader interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
}

type groupRecoveryProbeBalanceCache interface {
	InvalidateUserBalance(ctx context.Context, userID int64) error
}

type GroupRecoveryProbeBillingService struct {
	settingRepo  SettingRepository
	apiKeyRepo   groupRecoveryProbeAPIKeyReader
	accountRepo  groupRecoveryProbeAccountReader
	auditRepo    GroupRecoveryProbeBillingRepository
	atomicRepo   GroupRecoveryProbeAtomicSettlementRepository
	channelSvc   *ChannelService
	billingSvc   *BillingService
	balanceCache groupRecoveryProbeBalanceCache

	reservationMu sync.Mutex
	reservedUSD   float64
}

func NewGroupRecoveryProbeBillingService(
	settingRepo SettingRepository,
	apiKeyRepo APIKeyRepository,
	accountRepo AccountRepository,
	auditRepo GroupRecoveryProbeBillingRepository,
	channelSvc *ChannelService,
	billingSvc *BillingService,
	balanceCache *BillingCacheService,
) *GroupRecoveryProbeBillingService {
	service := &GroupRecoveryProbeBillingService{
		settingRepo:  settingRepo,
		apiKeyRepo:   apiKeyRepo,
		accountRepo:  accountRepo,
		auditRepo:    auditRepo,
		channelSvc:   channelSvc,
		billingSvc:   billingSvc,
		balanceCache: balanceCache,
	}
	service.atomicRepo, _ = auditRepo.(GroupRecoveryProbeAtomicSettlementRepository)
	return service
}

func defaultGroupRecoveryProbeBillingSettings() GroupRecoveryProbeBillingSettings {
	return GroupRecoveryProbeBillingSettings{
		DailyBudgetUSD:     groupRecoveryProbeDefaultDailyBudgetUSD,
		PerAttemptLimitUSD: groupRecoveryProbeDefaultPerAttemptLimitUSD,
	}
}

func (s *GroupRecoveryProbeBillingService) GetSettings(ctx context.Context) (GroupRecoveryProbeBillingSettings, error) {
	settings := defaultGroupRecoveryProbeBillingSettings()
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyGroupRecoveryProbeBilling)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return settings, nil
		}
		return settings, fmt.Errorf("get recovery probe billing settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, fmt.Errorf("parse recovery probe billing settings: %w", err)
	}
	normalizeGroupRecoveryProbeBillingSettings(&settings)
	if settings.APIKeyID > 0 && s.apiKeyRepo != nil {
		if apiKey, loadErr := s.apiKeyRepo.GetByID(ctx, settings.APIKeyID); loadErr == nil && apiKey != nil {
			settings.APIKeyName = apiKey.Name
		}
	}
	return settings, nil
}

func (s *GroupRecoveryProbeBillingService) UpdateSettings(ctx context.Context, ownerUserID int64, settings GroupRecoveryProbeBillingSettings) (GroupRecoveryProbeBillingSettings, error) {
	if s == nil || s.settingRepo == nil {
		return settings, fmt.Errorf("recovery probe billing service is unavailable")
	}
	if ownerUserID <= 0 {
		return settings, infraerrors.BadRequest("INVALID_PROBE_BILLING_OWNER", "owner user is required")
	}
	normalizeGroupRecoveryProbeBillingSettings(&settings)
	if settings.DailyBudgetUSD <= 0 || settings.DailyBudgetUSD > groupRecoveryProbeMaxDailyBudgetUSD {
		return settings, infraerrors.BadRequest("INVALID_PROBE_DAILY_BUDGET", "daily_budget_usd must be greater than 0 and no more than 1000")
	}
	if settings.PerAttemptLimitUSD <= 0 || settings.PerAttemptLimitUSD > settings.DailyBudgetUSD {
		return settings, infraerrors.BadRequest("INVALID_PROBE_ATTEMPT_LIMIT", "per_attempt_limit_usd must be greater than 0 and no more than daily_budget_usd")
	}
	if settings.Enabled && settings.APIKeyID <= 0 {
		return settings, infraerrors.BadRequest("INVALID_PROBE_BILLING_API_KEY", "api_key_id is required")
	}
	var apiKey *APIKey
	if settings.APIKeyID > 0 {
		if s.apiKeyRepo == nil {
			return settings, fmt.Errorf("recovery probe billing api key repository is unavailable")
		}
		var err error
		apiKey, err = s.apiKeyRepo.GetByID(ctx, settings.APIKeyID)
		if err != nil {
			return settings, fmt.Errorf("get probe billing api key: %w", err)
		}
		if apiKey == nil || apiKey.UserID != ownerUserID {
			return settings, infraerrors.Forbidden("PROBE_BILLING_API_KEY_NOT_OWNED", "the selected api key must belong to the current administrator")
		}
	}
	settings.OwnerUserID = ownerUserID
	settings.APIKeyName = ""
	data, err := json.Marshal(settings)
	if err != nil {
		return settings, fmt.Errorf("marshal recovery probe billing settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGroupRecoveryProbeBilling, string(data)); err != nil {
		return settings, fmt.Errorf("save recovery probe billing settings: %w", err)
	}
	if apiKey != nil {
		settings.APIKeyName = apiKey.Name
	}
	return settings, nil
}

func normalizeGroupRecoveryProbeBillingSettings(settings *GroupRecoveryProbeBillingSettings) {
	if settings == nil {
		return
	}
	if settings.DailyBudgetUSD <= 0 {
		settings.DailyBudgetUSD = groupRecoveryProbeDefaultDailyBudgetUSD
	}
	if settings.PerAttemptLimitUSD <= 0 {
		settings.PerAttemptLimitUSD = groupRecoveryProbeDefaultPerAttemptLimitUSD
	}
	settings.DailyBudgetUSD = roundProbeCost(settings.DailyBudgetUSD)
	settings.PerAttemptLimitUSD = roundProbeCost(settings.PerAttemptLimitUSD)
}

func (s *GroupRecoveryProbeBillingService) GetStatus(ctx context.Context, groupID int64) (*GroupRecoveryProbeBillingStatus, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	status := &GroupRecoveryProbeBillingStatus{Settings: settings}
	if s == nil || s.auditRepo == nil {
		status.RemainingUSD = settings.DailyBudgetUSD
		return status, nil
	}
	today := timezone.Today()
	status.GlobalToday, err = s.auditRepo.GetBillingSummary(ctx, 0, today)
	if err != nil {
		return nil, err
	}
	if groupID > 0 {
		status.GroupToday, err = s.auditRepo.GetBillingSummary(ctx, groupID, today)
		if err != nil {
			return nil, err
		}
	}
	status.RemainingUSD = math.Max(0, settings.DailyBudgetUSD-groupRecoveryProbeBudgetCost(status.GlobalToday))
	return status, nil
}

type GroupRecoveryProbeBillingReservation struct {
	service  *GroupRecoveryProbeBillingService
	Settings GroupRecoveryProbeBillingSettings
	amount   float64
	once     sync.Once
}

func (r *GroupRecoveryProbeBillingReservation) Release() {
	if r == nil || r.service == nil || r.amount <= 0 {
		return
	}
	r.once.Do(func() {
		r.service.reservationMu.Lock()
		r.service.reservedUSD = math.Max(0, r.service.reservedUSD-r.amount)
		r.service.reservationMu.Unlock()
	})
}

func (s *GroupRecoveryProbeBillingService) Reserve(ctx context.Context, groupID int64, attempts int) (*GroupRecoveryProbeBillingReservation, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	reservation := &GroupRecoveryProbeBillingReservation{service: s, Settings: settings}
	if !settings.Enabled {
		return reservation, nil
	}
	if attempts < 1 {
		attempts = 1
	}
	if s.auditRepo == nil {
		return nil, fmt.Errorf("recovery probe billing repository is unavailable")
	}
	summary, err := s.auditRepo.GetBillingSummary(ctx, 0, timezone.Today())
	if err != nil {
		return nil, err
	}
	amount := settings.PerAttemptLimitUSD * float64(attempts)
	s.reservationMu.Lock()
	defer s.reservationMu.Unlock()
	if groupRecoveryProbeBudgetCost(summary)+s.reservedUSD+amount > settings.DailyBudgetUSD+1e-12 {
		return nil, ErrGroupRecoveryProbeBudgetExceeded
	}
	s.reservedUSD += amount
	reservation.amount = amount
	return reservation, nil
}

func groupRecoveryProbeBudgetCost(summary GroupRecoveryProbeBillingSummary) float64 {
	// Keep old repository implementations and test doubles safe while the new
	// budget-aware aggregate is rolled out. Production repositories populate
	// TodayBudgetCost even when a settlement is still pending or failed.
	if summary.TodayBudgetCost > 0 {
		return summary.TodayBudgetCost
	}
	return summary.TodaySettledCost
}

func (s *GroupRecoveryProbeBillingService) Settle(ctx context.Context, audit GroupRecoveryProbeAudit, reservation *GroupRecoveryProbeBillingReservation) error {
	if s == nil || s.auditRepo == nil || audit.ID <= 0 {
		return nil
	}
	settings := defaultGroupRecoveryProbeBillingSettings()
	if reservation != nil {
		settings = reservation.Settings
	} else {
		loaded, err := s.GetSettings(ctx)
		if err != nil {
			return err
		}
		settings = loaded
	}
	if !settings.Enabled {
		return s.auditRepo.UpdateAuditSettlement(ctx, audit.ID, GroupRecoveryProbeAuditSettlement{
			Status:     GroupRecoveryProbeSettlementUnavailable,
			CostStatus: GroupRecoveryProbeCostStatusUnavailable,
		})
	}
	if !hasUsageTokens(audit.UsageTokens) {
		return s.auditRepo.UpdateAuditSettlement(ctx, audit.ID, GroupRecoveryProbeAuditSettlement{
			Status:     GroupRecoveryProbeSettlementUnavailable,
			CostStatus: GroupRecoveryProbeCostStatusUnavailable,
		})
	}
	atomicRepo := s.atomicRepo
	if atomicRepo == nil {
		atomicRepo, _ = s.auditRepo.(GroupRecoveryProbeAtomicSettlementRepository)
	}
	if s.apiKeyRepo == nil || s.accountRepo == nil || atomicRepo == nil || s.billingSvc == nil {
		return s.markSettlementFailed(ctx, audit.ID, "recovery probe settlement dependencies are unavailable")
	}
	apiKey, err := s.apiKeyRepo.GetByID(ctx, settings.APIKeyID)
	if err != nil || apiKey == nil || apiKey.UserID != settings.OwnerUserID || apiKey.User == nil {
		return s.markSettlementFailed(ctx, audit.ID, "probe billing api key or owner is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, audit.AccountID)
	if err != nil || account == nil {
		return s.markSettlementFailed(ctx, audit.ID, "probe target account is unavailable")
	}
	breakdown, err := s.billingSvc.CalculateCost(audit.Model, audit.UsageTokens, 1)
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return s.auditRepo.UpdateAuditSettlement(ctx, audit.ID, GroupRecoveryProbeAuditSettlement{
			Status:     GroupRecoveryProbeSettlementUnavailable,
			Error:      "model pricing is unavailable",
			CostStatus: GroupRecoveryProbeCostStatusUnavailable,
		})
	}
	accountMultiplier := account.BillingRateMultiplier()
	accountStatsCost := resolveAccountStatsCost(
		ctx,
		s.channelSvc,
		s.billingSvc,
		audit.AccountID,
		audit.GroupID,
		audit.Model,
		AccountStatsUsageContext{Tokens: audit.UsageTokens},
		breakdown.TotalCost,
	)
	accountCostBase := breakdown.TotalCost
	if accountStatsCost != nil {
		accountCostBase = *accountStatsCost
	}
	settledCost := roundProbeCost(accountCostBase * accountMultiplier)
	if settledCost <= 0 {
		return s.auditRepo.UpdateAuditSettlement(ctx, audit.ID, GroupRecoveryProbeAuditSettlement{
			Status:     GroupRecoveryProbeSettlementUnavailable,
			CostStatus: GroupRecoveryProbeCostStatusUnavailable,
		})
	}
	durationMs := int(audit.FinishedAt.Sub(audit.StartedAt).Milliseconds())
	requestID := fmt.Sprintf("probe:%d", audit.ID)
	rateMultiplier := 1.0
	if breakdown.TotalCost > 0 {
		rateMultiplier = settledCost / breakdown.TotalCost
	}
	billingMode := string(BillingModeToken)
	usageLog := &UsageLog{
		UserID:                apiKey.UserID,
		APIKeyID:              apiKey.ID,
		AccountID:             audit.AccountID,
		RequestID:             requestID,
		Model:                 audit.Model,
		RequestedModel:        audit.Model,
		GroupID:               &audit.GroupID,
		InputTokens:           audit.UsageTokens.InputTokens,
		OutputTokens:          audit.UsageTokens.OutputTokens,
		CacheCreationTokens:   audit.UsageTokens.CacheCreationTokens,
		CacheReadTokens:       audit.UsageTokens.CacheReadTokens,
		InputCost:             breakdown.InputCost,
		OutputCost:            breakdown.OutputCost,
		CacheCreationCost:     breakdown.CacheCreationCost,
		CacheReadCost:         breakdown.CacheReadCost,
		TotalCost:             breakdown.TotalCost,
		ActualCost:            settledCost,
		RateMultiplier:        rateMultiplier,
		AccountRateMultiplier: &accountMultiplier,
		AccountStatsCost:      accountStatsCost,
		BillingType:           BillingTypeBalance,
		BillingMode:           &billingMode,
		RequestType:           RequestTypeProbe,
		Stream:                false,
		DurationMs:            &durationMs,
		UserAgent:             stringPointer("sub2api-recovery-probe/1.0"),
		CreatedAt:             audit.FinishedAt,
	}
	cmd := &UsageBillingCommand{
		RequestID:           requestID,
		APIKeyID:            apiKey.ID,
		UserID:              apiKey.UserID,
		AccountID:           account.ID,
		AccountType:         account.Type,
		Model:               audit.Model,
		BillingType:         BillingTypeBalance,
		InputTokens:         audit.UsageTokens.InputTokens,
		OutputTokens:        audit.UsageTokens.OutputTokens,
		CacheCreationTokens: audit.UsageTokens.CacheCreationTokens,
		CacheReadTokens:     audit.UsageTokens.CacheReadTokens,
		BalanceCost:         settledCost,
		AccountQuotaCost:    settledCost,
	}
	cmd.Normalize()
	reservationAmount := settings.PerAttemptLimitUSD * float64(maxInt(audit.Attempts, 1))
	if reservation != nil && reservation.amount > 0 {
		reservationAmount = reservation.amount
	}
	settlementResult, err := atomicRepo.SettleProbe(ctx, &GroupRecoveryProbeAtomicSettlementCommand{
		AuditID:        audit.ID,
		ReservationUSD: roundProbeCost(reservationAmount),
		DailyBudgetUSD: settings.DailyBudgetUSD,
		BudgetSince:    timezone.Today(),
		SettledCostUSD: settledCost,
		BillingCommand: cmd,
		UsageLog:       usageLog,
	})
	if err != nil {
		return s.markSettlementFailed(ctx, audit.ID, err.Error())
	}
	if settlementResult != nil && settlementResult.BillingApplied && s.balanceCache != nil {
		_ = s.balanceCache.InvalidateUserBalance(ctx, apiKey.UserID)
	}
	return nil
}

func (s *GroupRecoveryProbeBillingService) markSettlementFailed(ctx context.Context, auditID int64, message string) error {
	message = sanitizeGroupRecoveryProbeError(message)
	updateErr := s.auditRepo.UpdateAuditSettlement(ctx, auditID, GroupRecoveryProbeAuditSettlement{
		Status: GroupRecoveryProbeSettlementFailed,
		Error:  message,
	})
	if updateErr != nil {
		return updateErr
	}
	return errors.New(message)
}

func roundProbeCost(value float64) float64 {
	return math.Round(value*1e10) / 1e10
}

func stringPointer(value string) *string {
	return &value
}
