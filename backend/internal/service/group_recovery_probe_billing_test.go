package service

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type recoveryProbeBillingSettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (r *recoveryProbeBillingSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *recoveryProbeBillingSettingRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

type recoveryProbeBillingAPIKeyRepoStub struct {
	keys map[int64]*APIKey
}

func (r *recoveryProbeBillingAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	return r.keys[id], nil
}

type recoveryProbeBillingAccountRepoStub struct {
	accounts map[int64]*Account
}

func (r *recoveryProbeBillingAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accounts[id], nil
}

type recoveryProbeBillingUsageRepoStub struct {
	mu       sync.Mutex
	logs     []*UsageLog
	inserted map[string]bool
}

func (r *recoveryProbeBillingUsageRepoStub) Create(_ context.Context, log *UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	key := log.RequestID + ":" + strconv.FormatInt(log.APIKeyID, 10)
	if r.inserted[key] {
		return false, nil
	}
	r.inserted[key] = true
	log.ID = int64(len(r.inserted))
	return true, nil
}

type recoveryProbeBillingRepoStub struct {
	UsageBillingRepository
	mu          sync.Mutex
	commands    []*UsageBillingCommand
	appliedKeys map[string]bool
}

type recoveryProbeAtomicSettlementRepoStub struct {
	mu       sync.Mutex
	commands []*GroupRecoveryProbeAtomicSettlementCommand
	applied  map[int64]bool
}

func (r *recoveryProbeAtomicSettlementRepoStub) SettleProbe(_ context.Context, cmd *GroupRecoveryProbeAtomicSettlementCommand) (*GroupRecoveryProbeAtomicSettlementResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, cmd)
	if r.applied[cmd.AuditID] {
		return &GroupRecoveryProbeAtomicSettlementResult{Status: GroupRecoveryProbeSettlementSettled}, nil
	}
	r.applied[cmd.AuditID] = true
	usageLogID := int64(len(r.applied))
	cmd.UsageLog.ID = usageLogID
	return &GroupRecoveryProbeAtomicSettlementResult{
		Status:         GroupRecoveryProbeSettlementSettled,
		BillingApplied: true,
		UsageLogID:     &usageLogID,
	}, nil
}

func (r *recoveryProbeBillingRepoStub) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyCmd := *cmd
	r.commands = append(r.commands, &copyCmd)
	key := cmd.RequestID + ":" + strconv.FormatInt(cmd.APIKeyID, 10)
	if r.appliedKeys[key] {
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.appliedKeys[key] = true
	return &UsageBillingApplyResult{Applied: true}, nil
}

type recoveryProbeBillingAuditRepoStub struct {
	mu          sync.Mutex
	summary     GroupRecoveryProbeBillingSummary
	settlements []GroupRecoveryProbeAuditSettlement
}

func (r *recoveryProbeBillingAuditRepoStub) GetBillingSummary(_ context.Context, _ int64, _ time.Time) (GroupRecoveryProbeBillingSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary, nil
}

func (r *recoveryProbeBillingAuditRepoStub) UpdateAuditSettlement(_ context.Context, _ int64, settlement GroupRecoveryProbeAuditSettlement) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settlements = append(r.settlements, settlement)
	return nil
}

type recoveryProbeBillingBalanceCacheStub struct {
	mu      sync.Mutex
	userIDs []int64
}

func (r *recoveryProbeBillingBalanceCacheStub) InvalidateUserBalance(_ context.Context, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userIDs = append(r.userIDs, userID)
	return nil
}

func TestGroupRecoveryProbeBillingUpdateSettingsValidatesOwnershipAndAllowsDisableWithoutKey(t *testing.T) {
	settingsRepo := &recoveryProbeBillingSettingRepoStub{values: make(map[string]string)}
	apiKeyRepo := &recoveryProbeBillingAPIKeyRepoStub{keys: map[int64]*APIKey{
		11: {ID: 11, UserID: 7, Name: "probe ledger"},
		12: {ID: 12, UserID: 8, Name: "foreign key"},
	}}
	svc := &GroupRecoveryProbeBillingService{settingRepo: settingsRepo, apiKeyRepo: apiKeyRepo}

	_, err := svc.UpdateSettings(context.Background(), 7, GroupRecoveryProbeBillingSettings{
		Enabled: true, APIKeyID: 12, DailyBudgetUSD: 1, PerAttemptLimitUSD: 0.01,
	})
	require.Error(t, err)

	updated, err := svc.UpdateSettings(context.Background(), 7, GroupRecoveryProbeBillingSettings{
		Enabled: true, APIKeyID: 11, DailyBudgetUSD: 1, PerAttemptLimitUSD: 0.01,
	})
	require.NoError(t, err)
	require.Equal(t, int64(7), updated.OwnerUserID)
	require.Equal(t, "probe ledger", updated.APIKeyName)

	disabled, err := svc.UpdateSettings(context.Background(), 7, GroupRecoveryProbeBillingSettings{Enabled: false})
	require.NoError(t, err)
	require.False(t, disabled.Enabled)
	require.Zero(t, disabled.APIKeyID)
	require.Equal(t, groupRecoveryProbeDefaultDailyBudgetUSD, disabled.DailyBudgetUSD)
}

func TestGroupRecoveryProbeBillingReserveHonorsSettledAndConcurrentReservations(t *testing.T) {
	settings := GroupRecoveryProbeBillingSettings{
		Enabled: true, OwnerUserID: 7, APIKeyID: 11, DailyBudgetUSD: 0.02, PerAttemptLimitUSD: 0.01,
	}
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	auditRepo := &recoveryProbeBillingAuditRepoStub{summary: GroupRecoveryProbeBillingSummary{TodaySettledCost: 0.005}}
	svc := &GroupRecoveryProbeBillingService{
		settingRepo: &recoveryProbeBillingSettingRepoStub{values: map[string]string{SettingKeyGroupRecoveryProbeBilling: string(encoded)}},
		auditRepo:   auditRepo,
	}

	first, err := svc.Reserve(context.Background(), 3, 1)
	require.NoError(t, err)
	require.NotNil(t, first)

	_, err = svc.Reserve(context.Background(), 3, 1)
	require.ErrorIs(t, err, ErrGroupRecoveryProbeBudgetExceeded)

	first.Release()
	second, err := svc.Reserve(context.Background(), 3, 1)
	require.NoError(t, err)
	second.Release()
}

func TestGroupRecoveryProbeBillingBudgetIncludesUnsettledEstimatedCost(t *testing.T) {
	settings := GroupRecoveryProbeBillingSettings{
		Enabled: true, OwnerUserID: 7, APIKeyID: 11, DailyBudgetUSD: 0.02, PerAttemptLimitUSD: 0.005,
	}
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	auditRepo := &recoveryProbeBillingAuditRepoStub{summary: GroupRecoveryProbeBillingSummary{
		TodaySettledCost: 0.004,
		TodayBudgetCost:  0.018,
	}}
	svc := &GroupRecoveryProbeBillingService{
		settingRepo: &recoveryProbeBillingSettingRepoStub{values: map[string]string{SettingKeyGroupRecoveryProbeBilling: string(encoded)}},
		auditRepo:   auditRepo,
	}

	status, err := svc.GetStatus(context.Background(), 0)
	require.NoError(t, err)
	require.InDelta(t, 0.002, status.RemainingUSD, 1e-12)

	_, err = svc.Reserve(context.Background(), 3, 1)
	require.ErrorIs(t, err, ErrGroupRecoveryProbeBudgetExceeded)
}

func TestGroupRecoveryProbeBillingSettleChargesCostOnceAndWritesProbeUsage(t *testing.T) {
	rate := 0.5
	owner := &User{ID: 7, Role: RoleAdmin}
	apiKey := &APIKey{ID: 11, UserID: owner.ID, Name: "probe ledger", User: owner}
	account := &Account{ID: 19, Type: AccountTypeAPIKey, RateMultiplier: &rate}
	usageRepo := &recoveryProbeBillingUsageRepoStub{inserted: make(map[string]bool)}
	billingRepo := &recoveryProbeBillingRepoStub{appliedKeys: make(map[string]bool)}
	auditRepo := &recoveryProbeBillingAuditRepoStub{}
	atomicRepo := &recoveryProbeAtomicSettlementRepoStub{applied: make(map[int64]bool)}
	balanceCache := &recoveryProbeBillingBalanceCacheStub{}
	svc := &GroupRecoveryProbeBillingService{
		apiKeyRepo:   &recoveryProbeBillingAPIKeyRepoStub{keys: map[int64]*APIKey{apiKey.ID: apiKey}},
		accountRepo:  &recoveryProbeBillingAccountRepoStub{accounts: map[int64]*Account{account.ID: account}},
		auditRepo:    auditRepo,
		atomicRepo:   atomicRepo,
		billingSvc:   NewBillingService(&config.Config{}, nil),
		balanceCache: balanceCache,
	}
	reservation := &GroupRecoveryProbeBillingReservation{Settings: GroupRecoveryProbeBillingSettings{
		Enabled: true, OwnerUserID: owner.ID, APIKeyID: apiKey.ID,
	}}
	startedAt := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	audit := GroupRecoveryProbeAudit{
		ID: 41, GroupID: 3, AccountID: account.ID, Model: "gpt-5.6-sol",
		Status: GroupRecoveryProbeStatusFailed, StartedAt: startedAt, FinishedAt: startedAt.Add(2 * time.Second),
		UsageTokens: UsageTokens{InputTokens: 100, OutputTokens: 10},
	}

	require.NoError(t, svc.Settle(context.Background(), audit, reservation))
	require.NoError(t, svc.Settle(context.Background(), audit, reservation))

	require.Empty(t, billingRepo.commands)
	require.Len(t, atomicRepo.commands, 2)
	command := atomicRepo.commands[0]
	require.Equal(t, "probe:41", command.BillingCommand.RequestID)
	require.InDelta(t, 0.0004, command.BillingCommand.BalanceCost, 1e-12)
	require.InDelta(t, 0.0004, command.BillingCommand.AccountQuotaCost, 1e-12)
	require.Zero(t, command.BillingCommand.APIKeyQuotaCost)
	require.Zero(t, command.BillingCommand.APIKeyRateLimitCost)

	require.Empty(t, usageRepo.logs)
	log := command.UsageLog
	require.Equal(t, RequestTypeProbe, log.RequestType)
	require.False(t, log.Stream)
	require.Equal(t, apiKey.ID, log.APIKeyID)
	require.Equal(t, owner.ID, log.UserID)
	require.InDelta(t, 0.0008, log.TotalCost, 1e-12)
	require.InDelta(t, 0.0004, log.ActualCost, 1e-12)
	require.InDelta(t, 0.5, log.RateMultiplier, 1e-12)

	require.Len(t, balanceCache.userIDs, 1)
	require.Equal(t, owner.ID, balanceCache.userIDs[0])
	require.Empty(t, auditRepo.settlements)
}

func TestGroupRecoveryProbeBillingSettleWithoutTokensDoesNotCharge(t *testing.T) {
	auditRepo := &recoveryProbeBillingAuditRepoStub{}
	svc := &GroupRecoveryProbeBillingService{auditRepo: auditRepo}
	reservation := &GroupRecoveryProbeBillingReservation{Settings: GroupRecoveryProbeBillingSettings{Enabled: true}}

	require.NoError(t, svc.Settle(context.Background(), GroupRecoveryProbeAudit{ID: 52}, reservation))
	require.Len(t, auditRepo.settlements, 1)
	require.Equal(t, GroupRecoveryProbeSettlementUnavailable, auditRepo.settlements[0].Status)
	require.Equal(t, GroupRecoveryProbeCostStatusUnavailable, auditRepo.settlements[0].CostStatus)
}
