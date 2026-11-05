package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelV3PublicRepo struct {
	ChannelMonitorV3Repository
	groups []ChannelMonitorV3GroupConfig
}

func (r *channelV3PublicRepo) ListGroups(context.Context) ([]ChannelMonitorV3GroupConfig, error) {
	return r.groups, nil
}

type channelV3PublicQuality struct{}

func (r channelV3PublicQuality) GetChannelMonitorV3GroupQualityWindows(_ context.Context, ids []int64) (map[int64]ChannelMonitorV3GroupQualityWindows, error) {
	duration := 10000.0
	window := AccountQualityWindow{SampleCount: 1, AverageDurationMs: &duration}
	return map[int64]ChannelMonitorV3GroupQualityWindows{1: {Last10: window, Last100: window}}, nil
}

func TestChannelMonitorV3PublicVisibilityAndMetadata(t *testing.T) {
	repo := &channelV3PublicRepo{groups: []ChannelMonitorV3GroupConfig{
		{GroupID: 1, Name: "Visible", Status: StatusActive, Visible: true, TestModel: "private-model", ProbeIntervalSeconds: 600},
		{GroupID: 2, Name: "Hidden", Status: StatusActive, Visible: false},
		{GroupID: 3, Name: "Inactive", Status: "disabled", Visible: true},
	}}
	svc := NewChannelMonitorV3Service(repo, NewChannelMonitorV3QualityService(channelV3PublicQuality{}))
	out, err := svc.Public(context.Background())
	require.NoError(t, err)
	require.Len(t, out.Groups, 1)
	require.EqualValues(t, 1, out.Groups[0].Last10.SampleCount)
	require.NotEmpty(t, out.Groups[0].Last10.Grade)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, field := range []string{"private-model", "test_model", "probe_interval", "first_token", "duration", "p50", "p90", "rate_multiplier", "user_id", "Hidden", "Inactive"} {
		require.NotContains(t, string(raw), field)
	}
	repo.groups[0].Visible = false
	out, err = svc.Public(context.Background())
	require.NoError(t, err)
	require.Empty(t, out.Groups)
}
