// Journal v1 is a disabled, locally reviewable application integration draft.
package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
	"strings"
	"time"
)

var ErrUsageJournalStableKeyRequired = errors.New("usage journal requires a stable keyed billing transaction")
var ErrUsageJournalLegacyMarker = errors.New("usage journal missing for preexisting marker; do not replay billing")

func usageJournalDraftEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Gateway.UsageJournalDraftEnabled
}

// UsageJournalLogSnapshot has an explicit scalar allowlist; relation objects are excluded.
type UsageJournalLogSnapshot struct {
	UserID                    int64
	APIKeyID                  int64
	AccountID                 int64
	RequestID                 string
	Model                     string
	RequestedModel            string
	UpstreamModel             *string
	UpstreamResponseModel     *string
	UpstreamModelMismatch     *bool
	ChannelID                 *int64
	ModelMappingChain         *string
	BillingTier               *string
	BillingMode               *string
	ServiceTier               *string
	ReasoningEffort           *string
	RequestedReasoningEffort  *string
	InboundEndpoint           *string
	UpstreamEndpoint          *string
	GroupID                   *int64
	SubscriptionID            *int64
	InputTokens               int
	OutputTokens              int
	CacheCreationTokens       int
	CacheReadTokens           int
	CacheCreation5mTokens     int
	CacheCreation1hTokens     int
	ImageInputTokens          int
	ImageInputCost            float64
	ImageOutputTokens         int
	ImageOutputCost           float64
	InputCost                 float64
	OutputCost                float64
	CacheCreationCost         float64
	CacheReadCost             float64
	TotalCost                 float64
	ActualCost                float64
	RateMultiplier            float64
	LongContextBillingApplied bool
	AccountRateMultiplier     *float64
	AccountStatsCost          *float64
	BillingType               int8
	RequestType               RequestType
	Stream                    bool
	OpenAIWSMode              bool
	NativeCompactionV2        bool
	DurationMs                *int
	FirstTokenMs              *int
	UserAgent                 *string
	IPAddress                 *string
	SessionID                 *string
	UpstreamRequestID         *string
	CacheTTLOverridden        bool
	ImageCount                int
	ImageSize                 *string
	ImageInputSize            *string
	ImageOutputSize           *string
	ImageSizeSource           *string
	ImageSizeBreakdown        map[string]int
	MediaType                 *string
	VideoCount                int
	VideoResolution           *string
	VideoDurationSeconds      *int
	CreatedAt                 time.Time
}
type UsageJournalSnapshot struct {
	Version            int
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	BillingCommandJSON json.RawMessage
	MonetaryParameters map[string]string
	UsageLogJSON       json.RawMessage
	SHA256             string
}

func NewUsageJournalSnapshot(cmd *UsageBillingCommand, log *UsageLog) (*UsageJournalSnapshot, error) {
	if cmd == nil || log == nil || cmd.RequestID == "" || strings.HasPrefix(cmd.RequestID, "generated:") || strings.HasPrefix(cmd.RequestID, "local:") || cmd.APIKeyID <= 0 || cmd.RequestID != log.RequestID || cmd.APIKeyID != log.APIKeyID || cmd.UserID != log.UserID || cmd.AccountID != log.AccountID || log.CreatedAt.IsZero() {
		return nil, ErrUsageJournalStableKeyRequired
	}
	cmd.Normalize()
	copyCommand := *cmd
	copyCommand.UsageJournalSnapshot = nil
	billing, e := json.Marshal(copyCommand)
	if e != nil {
		return nil, e
	}
	allowed := UsageJournalLogSnapshot{
		UserID:                    log.UserID,
		APIKeyID:                  log.APIKeyID,
		AccountID:                 log.AccountID,
		RequestID:                 log.RequestID,
		Model:                     log.Model,
		RequestedModel:            log.RequestedModel,
		UpstreamModel:             log.UpstreamModel,
		UpstreamResponseModel:     log.UpstreamResponseModel,
		UpstreamModelMismatch:     log.UpstreamModelMismatch,
		ChannelID:                 log.ChannelID,
		ModelMappingChain:         log.ModelMappingChain,
		BillingTier:               log.BillingTier,
		BillingMode:               log.BillingMode,
		ServiceTier:               log.ServiceTier,
		ReasoningEffort:           log.ReasoningEffort,
		RequestedReasoningEffort:  log.RequestedReasoningEffort,
		InboundEndpoint:           log.InboundEndpoint,
		UpstreamEndpoint:          log.UpstreamEndpoint,
		GroupID:                   log.GroupID,
		SubscriptionID:            log.SubscriptionID,
		InputTokens:               log.InputTokens,
		OutputTokens:              log.OutputTokens,
		CacheCreationTokens:       log.CacheCreationTokens,
		CacheReadTokens:           log.CacheReadTokens,
		CacheCreation5mTokens:     log.CacheCreation5mTokens,
		CacheCreation1hTokens:     log.CacheCreation1hTokens,
		ImageInputTokens:          log.ImageInputTokens,
		ImageInputCost:            log.ImageInputCost,
		ImageOutputTokens:         log.ImageOutputTokens,
		ImageOutputCost:           log.ImageOutputCost,
		InputCost:                 log.InputCost,
		OutputCost:                log.OutputCost,
		CacheCreationCost:         log.CacheCreationCost,
		CacheReadCost:             log.CacheReadCost,
		TotalCost:                 log.TotalCost,
		ActualCost:                log.ActualCost,
		RateMultiplier:            log.RateMultiplier,
		LongContextBillingApplied: log.LongContextBillingApplied,
		AccountRateMultiplier:     log.AccountRateMultiplier,
		AccountStatsCost:          log.AccountStatsCost,
		BillingType:               log.BillingType,
		RequestType:               log.RequestType,
		Stream:                    log.Stream,
		OpenAIWSMode:              log.OpenAIWSMode,
		NativeCompactionV2:        log.NativeCompactionV2,
		DurationMs:                log.DurationMs,
		FirstTokenMs:              log.FirstTokenMs,
		UserAgent:                 log.UserAgent,
		IPAddress:                 log.IPAddress,
		SessionID:                 log.SessionID,
		UpstreamRequestID:         log.UpstreamRequestID,
		CacheTTLOverridden:        log.CacheTTLOverridden,
		ImageCount:                log.ImageCount,
		ImageSize:                 log.ImageSize,
		ImageInputSize:            log.ImageInputSize,
		ImageOutputSize:           log.ImageOutputSize,
		ImageSizeSource:           log.ImageSizeSource,
		ImageSizeBreakdown:        log.ImageSizeBreakdown,
		MediaType:                 log.MediaType,
		VideoCount:                log.VideoCount,
		VideoResolution:           log.VideoResolution,
		VideoDurationSeconds:      log.VideoDurationSeconds,
		CreatedAt:                 log.CreatedAt,
	}
	allowed.CreatedAt = allowed.CreatedAt.UTC()
	encoded, e := json.Marshal(allowed)
	if e != nil {
		return nil, e
	}
	money := map[string]string{}
	for name, v := range map[string]float64{"balance_cost": cmd.BalanceCost, "subscription_cost": cmd.SubscriptionCost, "api_key_quota_cost": cmd.APIKeyQuotaCost, "api_key_rate_limit_cost": cmd.APIKeyRateLimitCost, "account_quota_cost": cmd.AccountQuotaCost} {
		money[name] = decimal.NewFromFloat(v).StringFixed(UsageBillingMonetaryScale)
	}
	result := &UsageJournalSnapshot{Version: 1, RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID, RequestFingerprint: cmd.RequestFingerprint, BillingCommandJSON: billing, MonetaryParameters: money, UsageLogJSON: encoded}
	h, e := result.contentHash()
	if e != nil {
		return nil, e
	}
	result.SHA256 = h
	return result, nil
}
func (s *UsageJournalSnapshot) contentHash() (string, error) {
	if s == nil {
		return "", errors.New("nil journal snapshot")
	}
	c := *s
	c.SHA256 = ""
	b, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (s *UsageJournalSnapshot) Validate(cmd *UsageBillingCommand) error {
	if s == nil || s.Version != 1 || cmd == nil || s.RequestID != cmd.RequestID || s.APIKeyID != cmd.APIKeyID || s.RequestFingerprint != cmd.RequestFingerprint {
		return ErrUsageBillingRequestConflict
	}
	h, e := s.contentHash()
	if e != nil {
		return e
	}
	if h != s.SHA256 {
		return errors.New("usage journal snapshot hash mismatch")
	}
	copyCommand := *cmd
	copyCommand.UsageJournalSnapshot = nil
	b, e := json.Marshal(copyCommand)
	if e != nil {
		return e
	}
	if !bytes.Equal(b, s.BillingCommandJSON) {
		return fmt.Errorf("usage journal billing parameters differ")
	}
	for name, v := range map[string]float64{"balance_cost": cmd.BalanceCost, "subscription_cost": cmd.SubscriptionCost, "api_key_quota_cost": cmd.APIKeyQuotaCost, "api_key_rate_limit_cost": cmd.APIKeyRateLimitCost, "account_quota_cost": cmd.AccountQuotaCost} {
		if s.MonetaryParameters[name] != decimal.NewFromFloat(v).StringFixed(UsageBillingMonetaryScale) {
			return errors.New("usage journal monetary parameters differ")
		}
	}
	log, e := s.DecodeLog()
	if e != nil {
		return e
	}
	if log.UserID != cmd.UserID || log.AccountID != cmd.AccountID || log.Model != cmd.Model || log.InputTokens != cmd.InputTokens || log.OutputTokens != cmd.OutputTokens || log.CacheCreationTokens != cmd.CacheCreationTokens || log.CacheReadTokens != cmd.CacheReadTokens || log.ImageCount != cmd.ImageCount {
		return errors.New("usage journal log billing identity differs")
	}
	text := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	if log.BillingType != cmd.BillingType || valueOrZero(log.SubscriptionID) != valueOrZero(cmd.SubscriptionID) || text(log.ServiceTier) != cmd.ServiceTier || text(log.ReasoningEffort) != cmd.ReasoningEffort {
		return errors.New("usage journal billing association differs")
	}
	// Preserve the existing distinct storage scales: raw usage cost remains ten
	// decimals; selected billing SQL parameters are normalized once to eight.
	actual := QuantizeUsageBillingAmount(log.ActualCost)
	if (cmd.BalanceCost > 0 && actual != cmd.BalanceCost) || (cmd.SubscriptionCost > 0 && actual != cmd.SubscriptionCost) {
		return errors.New("usage journal selected billing cost differs")
	}
	return nil
}
func (s *UsageJournalSnapshot) DecodeLog() (*UsageLog, error) {
	if s == nil {
		return nil, errors.New("nil journal snapshot")
	}
	h, e := s.contentHash()
	if e != nil || h != s.SHA256 {
		return nil, errors.New("usage journal snapshot hash mismatch")
	}
	var allowed UsageJournalLogSnapshot
	d := json.NewDecoder(bytes.NewReader(s.UsageLogJSON))
	d.DisallowUnknownFields()
	if e = d.Decode(&allowed); e != nil {
		return nil, e
	}
	b, e := json.Marshal(allowed)
	if e != nil {
		return nil, e
	}
	var log UsageLog
	if e = json.Unmarshal(b, &log); e != nil {
		return nil, e
	}
	if log.RequestID != s.RequestID || log.APIKeyID != s.APIKeyID || log.CreatedAt.IsZero() {
		return nil, ErrUsageJournalStableKeyRequired
	}
	return &log, nil
}
