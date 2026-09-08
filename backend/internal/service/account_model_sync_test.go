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

type modelSyncAccountRepo struct {
	AccountRepository
	accounts   map[int64]*Account
	list       []Account
	responses  map[int64]*http.Response
	updatesMu  sync.Mutex
	updateData map[int64]map[string]any
}

func (r *modelSyncAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	account, ok := r.accounts[id]
	if !ok {
		return nil, errors.New("account not found")
	}
	return account, nil
}

func (r *modelSyncAccountRepo) List(_ context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	if params.Page > 1 {
		return nil, &pagination.PaginationResult{Total: int64(len(r.list)), Page: params.Page, PageSize: params.PageSize}, nil
	}
	return r.list, &pagination.PaginationResult{Total: int64(len(r.list)), Page: 1, PageSize: params.PageSize}, nil
}

func (r *modelSyncAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updatesMu.Lock()
	defer r.updatesMu.Unlock()
	if r.updateData == nil {
		r.updateData = make(map[int64]map[string]any)
	}
	r.updateData[id] = updates
	return nil
}

type modelSyncHTTPUpstream struct {
	responses map[int64]*http.Response
}

func (u *modelSyncHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	response, ok := u.responses[accountID]
	if !ok {
		return nil, errors.New("unexpected account")
	}
	return response, nil
}

func (u *modelSyncHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func modelSyncResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newModelSyncAccount(id int64, extra map[string]any) *Account {
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

func TestExtractModelIDsNormalizesAndDeduplicatesModels(t *testing.T) {
	payload := map[string]any{
		"data": []any{
			map[string]any{"id": "gpt-5.6"},
			map[string]any{"id": "models/gemini-2.5-pro"},
			map[string]any{"id": "gpt-5.6"},
		},
		"results": []any{map[string]any{"model": "claude-sonnet-4-6"}},
	}

	require.Equal(t, []string{"claude-sonnet-4-6", "gemini-2.5-pro", "gpt-5.6"}, extractModelIDs(payload))
}

func TestSyncAccountModelsPersistsUpstreamSnapshot(t *testing.T) {
	account := newModelSyncAccount(1, nil)
	repo := &modelSyncAccountRepo{
		accounts:  map[int64]*Account{account.ID: account},
		responses: map[int64]*http.Response{account.ID: modelSyncResponse(http.StatusOK, `{"data":[{"id":"gpt-5.6"},{"id":"gpt-5.6"}]}`)},
	}
	upstream := &modelSyncHTTPUpstream{responses: repo.responses}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}

	result, err := svc.SyncAccountModels(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, accountModelsSyncSuccess, result.Status)
	require.Equal(t, []string{"gpt-5.6"}, result.Models)
	require.Equal(t, []string{"gpt-5.6"}, repo.updateData[account.ID][AccountAvailableModelsExtraKey])
	require.Equal(t, accountModelsSyncSuccess, repo.updateData[account.ID][accountModelsSyncStatusKey])
}

func TestSyncAccountModelsFailureKeepsPreviousSnapshot(t *testing.T) {
	account := newModelSyncAccount(2, map[string]any{AccountAvailableModelsExtraKey: []any{"known-model"}})
	repo := &modelSyncAccountRepo{
		accounts:  map[int64]*Account{account.ID: account},
		responses: map[int64]*http.Response{account.ID: modelSyncResponse(http.StatusBadGateway, `{}`)},
	}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: &modelSyncHTTPUpstream{responses: repo.responses},
		cfg:          &config.Config{},
	}

	result, err := svc.SyncAccountModels(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, accountModelsSyncFailed, result.Status)
	require.Equal(t, []string{"known-model"}, repo.updateData[account.ID][AccountAvailableModelsExtraKey])
	require.Equal(t, "upstream_http_502", repo.updateData[account.ID][accountModelsSyncErrorKey])
}

func TestSyncAllAccountModelsContinuesAfterOneAccountFails(t *testing.T) {
	first := newModelSyncAccount(10, nil)
	second := newModelSyncAccount(11, map[string]any{AccountAvailableModelsExtraKey: []any{"known-model"}})
	repo := &modelSyncAccountRepo{
		accounts: map[int64]*Account{first.ID: first, second.ID: second},
		list:     []Account{*first, *second},
		responses: map[int64]*http.Response{
			first.ID:  modelSyncResponse(http.StatusOK, `{"data":[{"id":"gpt-5.6"}]}`),
			second.ID: modelSyncResponse(http.StatusServiceUnavailable, `{}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: &modelSyncHTTPUpstream{responses: repo.responses},
		cfg:          &config.Config{},
	}

	summary, err := svc.SyncAllAccountModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, summary.Total)
	require.Equal(t, 1, summary.Success)
	require.Equal(t, 1, summary.Failed)
	require.Len(t, repo.updateData, 2)
}
