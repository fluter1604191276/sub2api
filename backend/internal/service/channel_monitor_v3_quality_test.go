package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelMonitorV3QualityReaderStub struct {
	windows map[int64]ChannelMonitorV3GroupQualityWindows
}

func (s channelMonitorV3QualityReaderStub) GetChannelMonitorV3GroupQualityWindows(context.Context, []int64) (map[int64]ChannelMonitorV3GroupQualityWindows, error) {
	return s.windows, nil
}

func TestChannelMonitorV3QualityUsesOneSampleWithoutChangingAccountMinimum(t *testing.T) {
	latency := float64(800)
	svc := NewChannelMonitorV3QualityService(channelMonitorV3QualityReaderStub{windows: map[int64]ChannelMonitorV3GroupQualityWindows{
		7: {Last10: AccountQualityWindow{SampleCount: 1, FirstTokenSampleCount: 1, AverageFirstTokenMs: &latency, P50FirstTokenMs: &latency, P90FirstTokenMs: &latency}},
	}})
	got, err := svc.GroupQuality(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(1), got[0].Last10.SampleCount)
	require.Equal(t, "A", got[0].Last10.Grade)
	require.Empty(t, applyAccountQualityScore(AccountQualityWindow{SampleCount: 1, FirstTokenSampleCount: 1, AverageFirstTokenMs: &latency}).QualityGrade)
}
