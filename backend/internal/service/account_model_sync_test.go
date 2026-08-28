package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type accountModelSyncRepo struct {
	AccountRepository
	accounts map[int64]*Account
	list     []Account
	mu       sync.Mutex
	updates  map[int64]map[string]any
}

func (r *accountModelSyncRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	account, ok := r.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	return account, nil
}

func (r *accountModelSyncRepo) List(_ context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	if params.Page > 1 {
		return nil, &pagination.PaginationResult{Total: int64(len(r.list)), Page: params.Page, PageSize: params.PageSize}, nil
	}
	return r.list, &pagination.PaginationResult{Total: int64(len(r.list)), Page: 1, PageSize: params.PageSize}, nil
}

func (r *accountModelSyncRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	return nil
}

type accountModelSyncUpstream struct {
	responses map[int64]*http.Response
	errors    map[int64]error
}

func (u *accountModelSyncUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (u *accountModelSyncUpstream) DoWithTLS(_ *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	if err := u.errors[accountID]; err != nil {
		return nil, err
	}
	response, ok := u.responses[accountID]
	if !ok {
		return nil, errors.New("missing response")
	}
	return response, nil
}

func accountModelResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func newAccountModelSyncAccount(id int64, extra map[string]any) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://models.example/v1",
		},
		Extra: extra,
	}
}

func TestNormalizeAccountModelIDs(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, normalizeAccountModelIDs([]string{" b ", "a", "b", ""}))
}

func TestSyncAccountModelsPersistsSnapshot(t *testing.T) {
	account := newAccountModelSyncAccount(1, nil)
	repo := &accountModelSyncRepo{accounts: map[int64]*Account{1: account}}
	upstream := &accountModelSyncUpstream{responses: map[int64]*http.Response{
		1: accountModelResponse(http.StatusOK, `{"data":[{"id":"gpt-5.6"},{"id":"gpt-5.6"}]}`),
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}

	result, err := svc.SyncAccountModels(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, accountModelsSyncSuccess, result.Status)
	require.Equal(t, 1, result.ModelCount)
	require.Equal(t, []string{"gpt-5.6"}, repo.updates[1][AccountAvailableModelsExtraKey])
}

func TestSyncAccountModelsFailurePreservesPreviousSnapshot(t *testing.T) {
	account := newAccountModelSyncAccount(2, map[string]any{AccountAvailableModelsExtraKey: []any{"known-model"}})
	repo := &accountModelSyncRepo{accounts: map[int64]*Account{2: account}}
	svc := &AccountTestService{
		accountRepo: repo,
		httpUpstream: &accountModelSyncUpstream{responses: map[int64]*http.Response{
			2: accountModelResponse(http.StatusBadGateway, `{}`),
		}},
		cfg: &config.Config{},
	}

	result, err := svc.SyncAccountModels(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, accountModelsSyncFailed, result.Status)
	_, overwritten := repo.updates[2][AccountAvailableModelsExtraKey]
	require.False(t, overwritten, "failed sync must not overwrite the previous snapshot")
	require.Equal(t, []any{"known-model"}, account.Extra[AccountAvailableModelsExtraKey])
}
