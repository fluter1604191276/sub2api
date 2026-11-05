package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelMonitorV3RepoStub struct{ savedGroup ChannelMonitorV3GroupConfig }

func (s *channelMonitorV3RepoStub) Settings(context.Context) (ChannelMonitorV3Settings, error) {
	return DefaultChannelMonitorV3Settings(), nil
}
func (s *channelMonitorV3RepoStub) SaveSettings(_ context.Context, in ChannelMonitorV3Settings) (ChannelMonitorV3Settings, error) {
	return in, nil
}
func (s *channelMonitorV3RepoStub) ListGroups(context.Context) ([]ChannelMonitorV3GroupConfig, error) {
	return nil, nil
}
func (s *channelMonitorV3RepoStub) SaveGroup(_ context.Context, in ChannelMonitorV3GroupConfig) (*ChannelMonitorV3GroupConfig, error) {
	s.savedGroup = in
	return &in, nil
}

func TestChannelMonitorV3ValidatesSettingsAndGroupConfig(t *testing.T) {
	repo := &channelMonitorV3RepoStub{}
	svc := NewChannelMonitorV3Service(repo, nil)
	require.Equal(t, 2.0, DefaultChannelMonitorV3Settings().DailyBudgetUSD)
	for _, budget := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		_, err := svc.SaveSettings(context.Background(), ChannelMonitorV3Settings{DailyBudgetUSD: budget})
		require.Error(t, err)
	}
	for _, interval := range []int{1, 59, 86401} {
		_, err := svc.SaveGroup(context.Background(), ChannelMonitorV3GroupConfig{GroupID: 1, ProbeIntervalSeconds: interval, TestModel: "m"})
		require.Error(t, err)
	}
	for _, interval := range []int{0, 60, 86400} {
		_, err := svc.SaveGroup(context.Background(), ChannelMonitorV3GroupConfig{GroupID: 1, ProbeIntervalSeconds: interval, TestModel: "m"})
		require.NoError(t, err)
	}
	_, err := svc.SaveGroup(context.Background(), ChannelMonitorV3GroupConfig{GroupID: 1, ProbeIntervalSeconds: 60, TestModel: "   "})
	require.NoError(t, err)
	require.Empty(t, repo.savedGroup.TestModel)
}
