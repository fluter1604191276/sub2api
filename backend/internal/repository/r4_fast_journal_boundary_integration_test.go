//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// This independently exercises real database rows and the shared mapper.
// Service tests cover the journal switch at the actual billing Apply boundary;
// this test does not migrate or execute the draft journal's SQL table.
func TestR4StoredFastFlagsRemainInactiveInColdProjections(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, free := range []bool{false, true} {
			t.Run(fmt.Sprintf("force_%t_free_%t", force, free), func(t *testing.T) {
				ctx := context.Background()
				suffix := time.Now().UnixNano()
				name := fmt.Sprintf("r4-journal-projection-%d", suffix)
				group := mustCreateGroup(t, integrationEntClient, &service.Group{
					Name: name, Platform: service.PlatformOpenAI, RateMultiplier: 1.25,
					ForceOpenAIFast: force, FreeOpenAIFast: free,
				})
				user := mustCreateUser(t, integrationEntClient, &service.User{
					Email: fmt.Sprintf("r4-journal-projection-%d@example.com", suffix), Concurrency: 5,
				})
				groupID := group.ID
				keyValue := fmt.Sprintf("sk-synthetic-r4-%d", suffix)
				keys := NewAPIKeyRepository(integrationEntClient, integrationDB)
				key := &service.APIKey{UserID: user.ID, GroupID: &groupID, Key: keyValue, Name: name, Status: service.StatusActive}
				require.NoError(t, keys.Create(ctx, key))
				t.Cleanup(func() {
					for _, item := range []struct {
						query string
						arg   any
					}{
						{"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", keyValue},
						{"DELETE FROM api_keys WHERE id = $1", key.ID},
						{"DELETE FROM users WHERE id = $1", user.ID},
						{"DELETE FROM groups WHERE id = $1", group.ID},
					} {
						_, err := integrationDB.ExecContext(ctx, item.query, item.arg)
						require.NoError(t, err)
					}
				})
				groups := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
				assertInactive := func(g *service.Group) {
					t.Helper()
					require.NotNil(t, g)
					require.Equal(t, groupID, g.ID)
					require.True(t, g.Hydrated)
					require.False(t, g.ForceOpenAIFast)
					require.False(t, g.FreeOpenAIFast)
					require.Equal(t, 1.25, g.RateMultiplier)
				}
				for _, phase := range []string{"before_snapshot", "after_snapshot"} {
					cold, err := keys.GetByKeyForAuth(ctx, keyValue)
					require.NoError(t, err)
					assertInactive(cold.Group)
					loaded, err := groups.GetByIDLite(ctx, groupID)
					require.NoError(t, err)
					assertInactive(loaded)
					listed, page, err := groups.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.PlatformOpenAI, "", name, nil)
					require.NoError(t, err)
					require.EqualValues(t, 1, page.Total)
					require.Len(t, listed, 1)
					assertInactive(&listed[0])
					var storedForce, storedFree bool
					require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT force_openai_fast, free_openai_fast FROM groups WHERE id = $1", groupID).Scan(&storedForce, &storedFree))
					require.Equal(t, force, storedForce, phase)
					require.Equal(t, free, storedFree, phase)
					if phase == "before_snapshot" {
						cmd := &service.UsageBillingCommand{RequestID: name, APIKeyID: key.ID, UserID: user.ID, AccountID: 3, Model: "synthetic", BalanceCost: 2.5}
						log := &service.UsageLog{RequestID: name, APIKeyID: key.ID, UserID: user.ID, AccountID: 3, Model: "synthetic", ActualCost: 2.5, CreatedAt: time.Now().UTC(), APIKey: cold, Group: cold.Group}
						snapshot, err := service.NewUsageJournalSnapshot(cmd, log)
						require.NoError(t, err)
						require.NoError(t, snapshot.Validate(cmd))
						decoded, err := snapshot.DecodeLog()
						require.NoError(t, err)
						require.Nil(t, decoded.Group)
						require.Nil(t, decoded.APIKey)
						assertInactive(cold.Group)
					}
				}
			})
		}
	}
}
