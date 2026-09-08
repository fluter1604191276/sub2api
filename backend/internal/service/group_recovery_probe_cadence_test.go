package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHighFrequencyProbeCompletionKeepsConfiguredCadence(t *testing.T) {
	now := time.Now()
	job := GroupRecoveryProbeJob{Mode: GroupRecoveryProbeModeHighFrequency, IntervalSeconds: 15}
	for _, result := range []GroupRecoveryProbeRoundResult{
		{Attempts: 5, SuccessCount: 5},
		{Attempts: 5, SuccessCount: 1},
		{Attempts: 5, SuccessCount: 0, LastError: "temporary error"},
	} {
		completion := buildGroupRecoveryProbeCompletion(job, result, now)
		require.Equal(t, now.Add(15*time.Second), completion.NextProbeAt)
	}
	require.LessOrEqual(t, groupRecoveryProbeTickInterval, 15*time.Second)
	require.Equal(t, 15*time.Second, groupRecoveryProbeSmartEligibleInterval(job))
}
