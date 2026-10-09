package config

import (
	"strings"
	"testing"
)

func TestUsageJournalDraftStartupDefaultOff(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_USAGE_JOURNAL_DRAFT_ENABLED", "")
	cfg, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Gateway.UsageJournalDraftEnabled {
		t.Fatal("journal draft default enabled")
	}
}
func TestUsageJournalDraftStartupRequiresExplicitCapacity(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_USAGE_JOURNAL_DRAFT_ENABLED", "true")
	_, e := Load()
	if e == nil || !strings.Contains(e.Error(), "explicit positive") {
		t.Fatalf("unwired journal enabled: %v", e)
	}
}

func TestUsageJournalConfiguredEnableAllowsSchemaValidatedRuntime(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_USAGE_JOURNAL_DRAFT_ENABLED", "true")
	t.Setenv("GATEWAY_USAGE_JOURNAL_MAX_PENDING", "100")
	t.Setenv("GATEWAY_USAGE_JOURNAL_MAX_PENDING_AGE_SECONDS", "60")
	t.Setenv("GATEWAY_USAGE_JOURNAL_MAX_RELATION_BYTES", "10485760")
	cfg, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if !cfg.Gateway.UsageJournalDraftEnabled {
		t.Fatal("explicit enable not loaded")
	}
	cfg.Gateway.CodexImageGenerationBridgeEnabled = true
	if e = cfg.ValidateUsageJournal(); e == nil {
		t.Fatal("automatic image bridge permitted")
	}
	cfg.Gateway.CodexImageGenerationBridgeEnabled = false
	cfg.Gateway.UsageRecord.OverflowPolicy = UsageRecordOverflowPolicyDrop
	if e = cfg.ValidateUsageJournal(); e == nil {
		t.Fatal("drop overflow permitted")
	}
	cfg.Gateway.UsageRecord.OverflowPolicy = UsageRecordOverflowPolicySync
	cfg.RunMode = RunModeSimple
	if e = cfg.ValidateUsageJournal(); e == nil {
		t.Fatal("simple permitted")
	}
}
