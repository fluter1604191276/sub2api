package service

import "context"

// ChannelMonitorV3Identity is a purpose-bound, non-login monitor user.
type ChannelMonitorV3Identity struct {
	UserID  int64   `json:"user_id"`
	Balance float64 `json:"balance"`
}

type ChannelMonitorV3IdentityRepository interface {
	EnsureProbeIdentity(context.Context) (*ChannelMonitorV3Identity, error)
	ProbeGroupKey(context.Context, int64) (*APIKey, error)
}
