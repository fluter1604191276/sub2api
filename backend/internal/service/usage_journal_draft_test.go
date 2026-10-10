package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func journalDraftInput() (*UsageBillingCommand, *UsageLog) {
	cmd := &UsageBillingCommand{RequestID: "synthetic-draft", APIKeyID: 2, UserID: 1, AccountID: 3, Model: "synthetic-model", BalanceCost: 2.5}
	cmd.Normalize()
	log := &UsageLog{RequestID: cmd.RequestID, APIKeyID: 2, UserID: 1, AccountID: 3, Model: cmd.Model, ActualCost: 2.5, CreatedAt: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)}
	return cmd, log
}
func TestUsageJournalDraftSnapshotFreezesScalarPayload(t *testing.T) {
	cmd, log := journalDraftInput()
	log.APIKey = &APIKey{Key: "SYNTHETIC_CREDENTIAL_MUST_NOT_PERSIST"}
	s, e := NewUsageJournalSnapshot(cmd, log)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Validate(cmd); e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded), "SYNTHETIC_CREDENTIAL") {
		t.Fatal("relation credentials serialized")
	}
	log.ActualCost = 0
	decoded, e := s.DecodeLog()
	if e != nil {
		t.Fatal(e)
	}
	if decoded.ActualCost != 2.5 || decoded.APIKey != nil || s.MonetaryParameters["balance_cost"] != "2.50000000" {
		t.Fatal("snapshot changed or incomplete")
	}
	s.UsageLogJSON = []byte(`{"RequestID":"tampered"}`)
	if s.Validate(cmd) == nil {
		t.Fatal("tampered snapshot accepted")
	}
}
func TestUsageJournalDraftRejectsInvalidLogKey(t *testing.T) {
	cmd, log := journalDraftInput()
	log.RequestID = ""
	if _, e := NewUsageJournalSnapshot(cmd, log); !errors.Is(e, ErrUsageJournalStableKeyRequired) {
		t.Fatal(e)
	}
}

func TestUsageJournalRejectsGeneratedAndLocalKeys(t *testing.T) {
	for _, key := range []string{"generated:random", "local:random"} {
		cmd, log := journalDraftInput()
		cmd.RequestID = key
		log.RequestID = key
		if _, e := NewUsageJournalSnapshot(cmd, log); !errors.Is(e, ErrUsageJournalStableKeyRequired) {
			t.Fatal(key, e)
		}
	}
}

func TestUsageJournalRejectsSelfHashedAssociationMismatch(t *testing.T) {
	for _, name := range []string{"billing_type", "subscription", "service_tier", "reasoning", "actual_cost", "relation"} {
		t.Run(name, func(t *testing.T) {
			cmd, log := journalDraftInput()
			switch name {
			case "billing_type":
				log.BillingType = 1
			case "subscription":
				id := int64(9)
				log.SubscriptionID = &id
			case "service_tier":
				v := "priority"
				log.ServiceTier = &v
			case "reasoning":
				v := "high"
				log.ReasoningEffort = &v
			case "actual_cost":
				log.ActualCost = 3
			}
			s, e := NewUsageJournalSnapshot(cmd, log)
			if e != nil {
				t.Fatal(e)
			}
			if name == "relation" {
				var payload map[string]any
				if e = json.Unmarshal(s.UsageLogJSON, &payload); e != nil {
					t.Fatal(e)
				}
				payload["Account"] = map[string]any{"Credentials": "synthetic"}
				s.UsageLogJSON, e = json.Marshal(payload)
				if e != nil {
					t.Fatal(e)
				}
				s.SHA256, e = s.contentHash()
				if e != nil {
					t.Fatal(e)
				}
			}
			if s.Validate(cmd) == nil {
				t.Fatal("self-hashed invalid association accepted")
			}
		})
	}
}

type journalCaptureBillingRepo struct {
	UsageBillingRepository
	cmd *UsageBillingCommand
}

var errJournalCaptureStop = errors.New("synthetic stop after actual Apply boundary")

func (r *journalCaptureBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.cmd = cmd
	return nil, errJournalCaptureStop
}
func TestUsageJournalDraftHooksActualApplyEntryAndDefaultsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		_, log := journalDraftInput()
		p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2.5, ActualCost: 2.5}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3}}
		cfg := &config.Config{}
		cfg.Gateway.UsageJournalDraftEnabled = enabled
		repo := &journalCaptureBillingRepo{}
		_, e := applyUsageBilling(context.Background(), log.RequestID, log, p, &billingDeps{cfg: cfg}, repo)
		if !errors.Is(e, errJournalCaptureStop) {
			t.Fatal(e)
		}
		if (repo.cmd.UsageJournalSnapshot != nil) != enabled {
			t.Fatalf("enabled=%v snapshot=%v", enabled, repo.cmd.UsageJournalSnapshot != nil)
		}
	}
}

// The real Apply entry is captured before any database/upstream side effect.
// Models the r4 cold projection established by repository integration tests.
// Journal activation must not turn on
// unmapped routing/pricing flags or change the existing billing command.
func TestUsageJournalApplyPreservesR4ColdFastAndBillingContract(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("journal_%t", enabled), func(t *testing.T) {
			_, log := journalDraftInput()
			group := &Group{ID: 50, Platform: PlatformOpenAI, RateMultiplier: 1.25, Hydrated: true}
			groupID := group.ID
			key := &APIKey{ID: 2, UserID: 1, GroupID: &groupID, Group: group, User: &User{ID: 1, Status: StatusActive}, Status: StatusActive}
			log.Group, log.APIKey = group, key
			p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2.5}, User: key.User, APIKey: key, Account: &Account{ID: 3}}
			cfg := &config.Config{}
			// Build the off control in every subtest, including filtered runs.
			control := &journalCaptureBillingRepo{}
			_, controlErr := applyUsageBilling(context.Background(), log.RequestID, log, p, &billingDeps{cfg: cfg}, control)
			require.ErrorIs(t, controlErr, errJournalCaptureStop)
			require.NotNil(t, control.cmd)
			require.Nil(t, control.cmd.UsageJournalSnapshot)
			baseline := *control.cmd
			cfg.Gateway.UsageJournalDraftEnabled = enabled
			repo := &journalCaptureBillingRepo{}
			applied, err := applyUsageBilling(context.Background(), log.RequestID, log, p, &billingDeps{cfg: cfg}, repo)
			require.ErrorIs(t, err, errJournalCaptureStop)
			require.False(t, applied)
			require.NotNil(t, repo.cmd)
			require.False(t, group.ForceOpenAIFast)
			require.False(t, group.FreeOpenAIFast)
			require.Equal(t, 1.25, group.RateMultiplier)
			require.Same(t, group, key.Group)
			require.Equal(t, 2.5, repo.cmd.BalanceCost)
			snapshot := repo.cmd.UsageJournalSnapshot
			plain := *repo.cmd
			plain.UsageJournalSnapshot = nil
			if !enabled {
				require.Nil(t, snapshot)
			} else {
				require.Equal(t, baseline, plain, "journal must preserve amounts, identity and idempotency fingerprint")
				require.NotNil(t, snapshot)
				require.NoError(t, snapshot.Validate(repo.cmd))
				decoded, err := snapshot.DecodeLog()
				require.NoError(t, err)
				require.Nil(t, decoded.Group)
				require.Nil(t, decoded.APIKey)
				require.Equal(t, 2.5, decoded.ActualCost)
			}
			// A cold false auth projection must stay false across the real cache codec.
			svc := &APIKeyService{}
			payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), key)})
			require.NoError(t, err)
			var cached APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(payload, &cached))
			materialized, used, err := svc.applyAuthCacheEntry("synthetic", &cached)
			require.NoError(t, err)
			require.True(t, used)
			require.False(t, materialized.Group.ForceOpenAIFast)
			require.False(t, materialized.Group.FreeOpenAIFast)
			require.Equal(t, 1.25, materialized.Group.RateMultiplier)
		})
	}
}
