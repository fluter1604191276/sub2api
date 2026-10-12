package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountPriorityDecimalConversion(t *testing.T) {
	for _, test := range []struct {
		value  string
		want   int
		reason string
	}{
		{"0", 1, ""}, {"0.0001", 1, ""}, {"0.001", 1, ""}, {"0.0015", 2, ""},
		{"0.002", 2, ""}, {"1", 1000, ""}, {"2.0070", 2007, ""},
		{"999999.9999", 1000000000, ""}, {"2147483.647", 2147483647, ""},
		{"2147483.6471", 0, "priority_out_of_range"}, {"-0.001", 0, "negative_multiplier"},
		{"", 0, "invalid_multiplier"}, {"NaN", 0, "invalid_multiplier"}, {"Infinity", 0, "invalid_multiplier"}, {"bad", 0, "invalid_multiplier"},
	} {
		t.Run(test.value, func(t *testing.T) {
			_, priority, reason := priorityFromAccountMultiplier(&test.value)
			require.Equal(t, test.reason, reason)
			if reason == "" {
				require.NotNil(t, priority)
				require.Equal(t, test.want, *priority)
			} else {
				require.Nil(t, priority)
			}
		})
	}
	_, priority, reason := priorityFromAccountMultiplier(nil)
	require.Nil(t, priority)
	require.Equal(t, "missing_multiplier", reason)
}

type priorityConversionRepoStub struct {
	AccountRepository
	rows   []AccountPriorityConversionRow
	writes map[int64]int
	stale  map[int64]bool
	fail   map[int64]bool
}

func (r *priorityConversionRepoStub) GetPriorityConversionAccounts(context.Context, []int64) ([]AccountPriorityConversionRow, error) {
	return append([]AccountPriorityConversionRow(nil), r.rows...), nil
}
func (r *priorityConversionRepoStub) UpdatePriorityIfUnchanged(_ context.Context, id int64, _ string, _, priority int) (bool, error) {
	if r.fail[id] {
		return false, errors.New("database error with private data")
	}
	if r.stale[id] {
		return false, nil
	}
	r.writes[id] = priority
	return true, nil
}
func priorityFixtureRow(id int64, multiplier *string, old int) AccountPriorityConversionRow {
	return AccountPriorityConversionRow{AccountID: id, Name: "synthetic", RateMultiplier: multiplier, OldPriority: old}
}

func TestAccountPriorityPreviewAndApplyArePerAccount(t *testing.T) {
	a, b, zero, bad := "2.0070", "0.0015", "0", "-1"
	repo := &priorityConversionRepoStub{rows: []AccountPriorityConversionRow{
		priorityFixtureRow(1, &a, 7), priorityFixtureRow(2, &b, 50), priorityFixtureRow(3, &b, 19),
		priorityFixtureRow(4, &zero, 0), priorityFixtureRow(5, &bad, 50), priorityFixtureRow(6, nil, 50),
	}, writes: map[int64]int{}}
	svc := &adminServiceImpl{accountRepo: repo}
	preview, err := svc.PreviewAccountPriorityConversion(context.Background(), []int64{1, 2, 3, 4, 5, 6, 99, 1})
	require.NoError(t, err)
	require.Len(t, preview.Rows, 7)
	require.Empty(t, repo.writes, "preview/cancel must not write")
	require.Equal(t, "negative_multiplier", preview.Rows[4].Reason)
	require.Equal(t, "missing_multiplier", preview.Rows[5].Reason)
	require.Equal(t, "account_unavailable", preview.Rows[6].Reason)
	confirmations := []AccountPriorityConfirmation{{1, a, 7}, {2, b, 50}, {3, b, 19}, {4, zero, 0}, {5, bad, 50}, {6, "0", 50}, {99, "0", 50}, {1, a, 7}}
	result, err := svc.ApplyAccountPriorityConversion(context.Background(), confirmations)
	require.NoError(t, err)
	require.Equal(t, 4, result.Updated)
	require.Equal(t, map[int64]int{1: 2007, 2: 2, 3: 2, 4: 1}, repo.writes, "same values allowed, manual priorities overwritten independently")
	require.Equal(t, "2.0070", a)
	require.Equal(t, 7, repo.rows[0].OldPriority, "source snapshot is not rewritten")
}

func TestAccountPriorityChangesAndFailuresDoNotOverwrite(t *testing.T) {
	rate := "1.0000"
	repo := &priorityConversionRepoStub{rows: []AccountPriorityConversionRow{
		priorityFixtureRow(1, &rate, 50), priorityFixtureRow(2, &rate, 50), priorityFixtureRow(3, &rate, 50), priorityFixtureRow(4, &rate, 50), priorityFixtureRow(5, &rate, 50),
	}, writes: map[int64]int{}, stale: map[int64]bool{3: true}, fail: map[int64]bool{4: true}}
	svc := &adminServiceImpl{accountRepo: repo}
	result, err := svc.ApplyAccountPriorityConversion(context.Background(), []AccountPriorityConfirmation{{1, "2", 50}, {2, rate, 7}, {3, rate, 50}, {4, rate, 50}, {5, rate, 50}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Updated)
	require.Equal(t, map[int64]int{5: 1000}, repo.writes)
	for _, row := range result.Rows[:3] {
		require.Equal(t, "preview_changed", row.Reason)
	}
	require.Equal(t, "update_failed", result.Rows[3].Reason)
}
