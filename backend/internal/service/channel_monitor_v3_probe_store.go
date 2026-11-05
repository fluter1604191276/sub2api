package service

import (
	"context"
	"time"
)

type ChannelMonitorV3ProbeJob struct {
	DispatchID      string
	ClientRequestID string
	GroupID         int64
	Model           string
	Platform        string
	BudgetDay       string
	ReservedUSD     float64
	CreatedAt       time.Time
}

type ChannelMonitorV3Budget struct {
	Date         string  `json:"date"`
	SpentUSD     float64 `json:"spent_usd"`
	ReservedUSD  float64 `json:"reserved_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
}

type ChannelMonitorV3ProbeRepository interface {
	ListPendingProbes(context.Context) ([]ChannelMonitorV3ProbeJob, error)
	TryClaimProbe(context.Context, ChannelMonitorV3GroupConfig, time.Time, float64) (*ChannelMonitorV3ProbeJob, error)
	MarkProbeDispatched(context.Context, string) error
	FinishProbe(context.Context, string, bool) error
	ReleaseUndispatchedProbe(context.Context, string) error
	SettleProbe(context.Context, string, UpstreamAttemptCostSummary) error
	HasRealTrafficSince(context.Context, int64, time.Time) (bool, error)
	ProbeBudget(context.Context, string, float64) (ChannelMonitorV3Budget, error)
}
