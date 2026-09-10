package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type conditionalModelSyncRepo struct {
	*modelSyncAccountRepo
	writeErr error
	written  map[string]any
}

func (r *conditionalModelSyncRepo) UpdateCredentialsIfUnchanged(_ context.Context, id int64, credentials map[string]any, version time.Time) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	if !r.accounts[id].UpdatedAt.Equal(version) {
		return ErrAccountModelSyncConflict
	}
	r.written = credentials
	return nil
}

func TestApplyAccountModelMappingsUsesPreviewAndPreservesManualMappings(t *testing.T) {
	a := newModelSyncAccount(1, nil)
	a.UpdatedAt = time.Now().UTC()
	a.Credentials["model_mapping"] = map[string]any{"old": "old", "alias": "upstream", "*": "*", "new": "manual-target"}
	r := &conditionalModelSyncRepo{modelSyncAccountRepo: &modelSyncAccountRepo{accounts: map[int64]*Account{1: a}}}
	s := &AccountTestService{accountRepo: r}
	result := s.ApplyAccountModelMappings(context.Background(), []AccountModelSyncApplyItem{{AccountID: 1, Version: a.UpdatedAt.Format(time.RFC3339Nano), Models: []string{"new", "added"}, Mode: "sync"}})
	require.Equal(t, "applied", result[0].Status)
	require.Equal(t, map[string]any{"alias": "upstream", "*": "*", "new": "manual-target", "added": "added"}, r.written["model_mapping"])
	require.Equal(t, "test-key", r.written["api_key"])
	require.Contains(t, a.Credentials["model_mapping"], "old")
}

func TestApplyAccountModelMappingsSyncRemovesStaleAutomaticEntries(t *testing.T) {
	a := newModelSyncAccount(2, nil)
	a.UpdatedAt = time.Now().UTC()
	a.Credentials["model_mapping"] = map[string]any{"old": "old", "alias": "upstream", "*": "*"}
	r := &conditionalModelSyncRepo{modelSyncAccountRepo: &modelSyncAccountRepo{accounts: map[int64]*Account{2: a}}}
	s := &AccountTestService{accountRepo: r}
	result := s.ApplyAccountModelMappings(context.Background(), []AccountModelSyncApplyItem{{AccountID: 2, Version: a.UpdatedAt.Format(time.RFC3339Nano), Models: []string{"new"}, Mode: "sync"}})
	require.Equal(t, "applied", result[0].Status)
	require.Equal(t, map[string]any{"alias": "upstream", "*": "*", "new": "new"}, r.written["model_mapping"])
}

func TestApplyAccountModelMappingsReportsConcurrentConflict(t *testing.T) {
	a := newModelSyncAccount(1, nil)
	a.UpdatedAt = time.Now().UTC()
	r := &conditionalModelSyncRepo{modelSyncAccountRepo: &modelSyncAccountRepo{accounts: map[int64]*Account{1: a}}, writeErr: ErrAccountModelSyncConflict}
	s := &AccountTestService{accountRepo: r}
	result := s.ApplyAccountModelMappings(context.Background(), []AccountModelSyncApplyItem{{AccountID: 1, Version: a.UpdatedAt.Format(time.RFC3339Nano), Models: []string{"new"}}})
	require.Equal(t, "conflict", result[0].Status)
	require.Nil(t, r.written)
}

func TestApplyAccountModelMappingsRejectsStaleOrEmptySelection(t *testing.T) {
	a := newModelSyncAccount(1, nil)
	a.UpdatedAt = time.Now().UTC()
	r := &conditionalModelSyncRepo{modelSyncAccountRepo: &modelSyncAccountRepo{accounts: map[int64]*Account{1: a}}}
	s := &AccountTestService{accountRepo: r}
	result := s.ApplyAccountModelMappings(context.Background(), []AccountModelSyncApplyItem{
		{AccountID: 1, Version: "stale", Models: []string{"new"}},
		{AccountID: 1, Version: a.UpdatedAt.Format(time.RFC3339Nano)},
	})
	require.Equal(t, "conflict", result[0].Status)
	require.Equal(t, "failed", result[1].Status)
	require.Nil(t, r.written)
}
