package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type previewConcurrencyUpstream struct {
	mu                    sync.Mutex
	inFlight, maxInFlight int
}

func (u *previewConcurrencyUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.inFlight++
	if u.inFlight > u.maxInFlight {
		u.maxInFlight = u.inFlight
	}
	u.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	u.mu.Lock()
	u.inFlight--
	u.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(
		strings.NewReader(`{"data":[{"id":"model-` + fmt.Sprint(accountID) + `"}]}`),
	)}, nil
}

func (u *previewConcurrencyUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestPreviewAllAccountModelMappingsIsBoundedOrderedAndReadOnly(t *testing.T) {
	accounts := make([]Account, 12)
	repo := &modelSyncAccountRepo{accounts: map[int64]*Account{}, list: accounts}
	for i := range accounts {
		accounts[i] = *newModelSyncAccount(int64(i+1), nil)
		repo.accounts[int64(i+1)] = &accounts[i]
	}
	u := &previewConcurrencyUpstream{}
	s := &AccountTestService{accountRepo: repo, httpUpstream: u, cfg: &config.Config{}}
	preview, err := s.PreviewAllAccountModelMappings(context.Background())
	require.NoError(t, err)
	require.Len(t, preview.Results, 12)
	for i, entry := range preview.Results {
		require.Equal(t, int64(i+1), entry.AccountID)
		require.Equal(t, []string{"model-" + fmt.Sprint(i+1)}, entry.UpstreamModels)
	}
	require.LessOrEqual(t, u.maxInFlight, 5)
	require.Empty(t, repo.updateData)
}
