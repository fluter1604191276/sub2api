package service

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type iqAccessUsersStub struct {
	UserRepository
	users map[int64]*User
}

func (s *iqAccessUsersStub) GetByID(_ context.Context, id int64) (*User, error) {
	return s.users[id], nil
}

type iqAccessGroupsStub struct {
	GroupRepository
	groups []Group
}

func (s *iqAccessGroupsStub) ListActive(context.Context) ([]Group, error) { return s.groups, nil }
func (s *iqAccessGroupsStub) ListActiveByPlatform(_ context.Context, platform string) ([]Group, error) {
	out := make([]Group, 0, len(s.groups))
	for _, group := range s.groups {
		if group.Platform == platform {
			out = append(out, group)
		}
	}
	return out, nil
}

type iqAccessSubscriptionsStub struct {
	UserSubscriptionRepository
	byUser map[int64][]UserSubscription
}

func (s *iqAccessSubscriptionsStub) ListActiveByUserID(_ context.Context, userID int64) ([]UserSubscription, error) {
	return s.byUser[userID], nil
}

type iqAccessRepoStub struct {
	iqRunnerRepoStub
	visible     map[int64]IQResult
	allowed     []int64
	public      []IQPublicResult
	configured  []int64
	publicModel string
}

func (s *iqAccessRepoStub) LatestValid(_ context.Context, allowed, configured []int64, model, _, _ string) ([]IQPublicResult, error) {
	s.allowed = append([]int64(nil), allowed...)
	s.configured = append([]int64(nil), configured...)
	s.publicModel = model
	return s.public, nil
}

func (s *iqAccessRepoStub) Latest(_ context.Context, allowed []int64) (*IQView, error) {
	s.allowed = append([]int64(nil), allowed...)
	results := make([]IQResult, 0, len(allowed))
	for _, id := range allowed {
		if result, ok := s.visible[id]; ok {
			results = append(results, result)
		}
	}
	return &IQView{Results: results}, nil
}

func TestOpenAIIQPublicViewUsesRepositoryWhitelistAndDoesNotUseLatestRound(t *testing.T) {
	groups := []Group{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard}, {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard}}
	users := &iqAccessUsersStub{users: map[int64]*User{20: {ID: 20, Role: RoleUser}}}
	settings := iqRunnerSettings()
	settings.GroupIDs = []int64{1}
	repo := &iqAccessRepoStub{public: []IQPublicResult{{GroupID: 1, Status: "correct", Model: "gpt-6-astra"}, {GroupID: 2, Status: "no_results", Model: "gpt-6-astra"}}}
	keys := &APIKeyService{userRepo: users, groupRepo: &iqAccessGroupsStub{groups: groups}, userSubRepo: &iqAccessSubscriptionsStub{}}
	svc := NewOpenAIIQService(&iqRunnerSettingsStub{settings: settings}, &iqAccessGroupsStub{groups: groups}, users, keys, repo, nil, nil, nil, nil)
	view, err := svc.PublicView(context.Background(), 20)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, repo.allowed)
	require.Equal(t, []int64{1}, repo.configured)
	require.Equal(t, "gpt-6-astra", repo.publicModel)
	require.Equal(t, IQPublicSummary{Total: 2, Correct: 1, NoResults: 1}, view.Summary)
}

func TestOpenAIIQViewFiltersResultsByUserGroupAccess(t *testing.T) {
	groups := []Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription},
		{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard, IsExclusive: true},
		{ID: 4, Platform: PlatformAnthropic, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard},
	}
	users := &iqAccessUsersStub{users: map[int64]*User{
		10: {ID: 10, Role: RoleAdmin},
		20: {ID: 20, Role: RoleUser},
		30: {ID: 30, Role: RoleUser, AllowedGroups: []int64{3}},
	}}
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	groupRepo := &iqAccessGroupsStub{groups: groups}
	subs := &iqAccessSubscriptionsStub{byUser: map[int64][]UserSubscription{20: {{GroupID: 2}}}}
	keys := &APIKeyService{userRepo: users, groupRepo: groupRepo, userSubRepo: subs}

	for _, test := range []struct {
		name        string
		userID      int64
		want        []int64
		wantTotal   int
		wantCorrect int
	}{
		{"admin sees every active OpenAI group", 10, []int64{1, 2, 3}, 3, 1},
		{"ordinary user sees public standard and active subscription only", 20, []int64{1, 2}, 2, 1},
		{"allowed private group becomes visible", 30, []int64{1, 3}, 2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &iqAccessRepoStub{visible: map[int64]IQResult{
				1: {GroupID: 1, Status: "correct"}, 2: {GroupID: 2, Status: "incorrect"}, 3: {GroupID: 3, Status: "pending"}, 4: {GroupID: 4, Status: "error"},
			}}
			svc := NewOpenAIIQService(settings, groupRepo, users, keys, repo, nil, nil, nil, nil)
			view, err := svc.View(context.Background(), test.userID)
			require.NoError(t, err)
			sort.Slice(repo.allowed, func(i, j int) bool { return repo.allowed[i] < repo.allowed[j] })
			require.Equal(t, test.want, repo.allowed)
			require.Equal(t, test.wantTotal, view.Summary.Total)
			require.Equal(t, test.wantCorrect, view.Summary.Correct)
			_, err = svc.PublicView(context.Background(), test.userID)
			require.NoError(t, err)
			sort.Slice(repo.allowed, func(i, j int) bool { return repo.allowed[i] < repo.allowed[j] })
			require.Equal(t, test.want, repo.allowed, "public whitelist view must retain the same subscription/private-group policy")
		})
	}
}

func TestOpenAIIQPublicViewRejectsMissingUser(t *testing.T) {
	svc := NewOpenAIIQService(nil, nil, &iqAccessUsersStub{}, nil, &iqAccessRepoStub{}, nil, nil, nil, nil)
	_, err := svc.PublicView(context.Background(), 99)
	require.ErrorIs(t, err, ErrUserNotFound)
}
