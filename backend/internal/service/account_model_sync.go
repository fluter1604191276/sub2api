package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	accountModelsSyncedAtKey   = "available_models_synced_at"
	accountModelsSyncStatusKey = "available_models_sync_status"
	accountModelsSyncErrorKey  = "available_models_sync_error"
	accountModelsSyncSuccess   = "success"
	accountModelsSyncFailed    = "failed"
	accountModelsSyncSkipped   = "unsupported"
)

type AccountModelOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AccountModelSyncResult is intentionally display-safe. It never includes
// upstream URLs, credentials, headers, or response bodies.
type AccountModelSyncResult struct {
	AccountID  int64  `json:"account_id"`
	Status     string `json:"status"`
	ModelCount int    `json:"model_count"`
	Error      string `json:"error,omitempty"`
}

type AccountModelSyncSummary struct {
	Total       int                      `json:"total"`
	Success     int                      `json:"success"`
	Failed      int                      `json:"failed"`
	Unsupported int                      `json:"unsupported"`
	Results     []AccountModelSyncResult `json:"results"`
}

func normalizeAccountModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		normalized = append(normalized, model)
	}
	sort.Strings(normalized)
	return normalized
}

func readAccountModelIDs(raw any) []string {
	models := make([]string, 0)
	switch values := raw.(type) {
	case []string:
		models = append(models, values...)
	case []any:
		for _, value := range values {
			if model, ok := value.(string); ok {
				models = append(models, model)
			}
		}
	}
	return normalizeAccountModelIDs(models)
}

func safeAccountModelSyncError(err error) string {
	if err == nil {
		return ""
	}
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		switch syncErr.Kind {
		case UpstreamModelSyncErrorConfiguration:
			return "configuration_error"
		case UpstreamModelSyncErrorUnsupported:
			return "unsupported"
		default:
			return "upstream_error"
		}
	}
	return "sync_failed"
}

func (s *AccountTestService) syncAccountModels(ctx context.Context, account *Account) AccountModelSyncResult {
	result := AccountModelSyncResult{AccountID: account.ID}
	models, err := s.FetchUpstreamSupportedModels(ctx, account)
	if err != nil {
		result.Status = accountModelsSyncFailed
		result.Error = safeAccountModelSyncError(err)
		if result.Error == "unsupported" {
			result.Status = accountModelsSyncSkipped
		}
		// Preserve the previous known-good snapshot on every failure.
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
			accountModelsSyncedAtKey:   time.Now().UTC().Format(time.RFC3339),
			accountModelsSyncStatusKey: result.Status,
			accountModelsSyncErrorKey:  result.Error,
		})
		return result
	}

	models = normalizeAccountModelIDs(models)
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		AccountAvailableModelsExtraKey: models,
		accountModelsSyncedAtKey:       time.Now().UTC().Format(time.RFC3339),
		accountModelsSyncStatusKey:     accountModelsSyncSuccess,
		accountModelsSyncErrorKey:      "",
	}); err != nil {
		result.Status = accountModelsSyncFailed
		result.Error = "persist_failed"
		return result
	}
	result.Status = accountModelsSyncSuccess
	result.ModelCount = len(models)
	return result
}

func (s *AccountTestService) SyncAccountModels(ctx context.Context, accountID int64) (AccountModelSyncResult, error) {
	if s == nil || s.accountRepo == nil {
		return AccountModelSyncResult{}, errors.New("account model sync is not configured")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return AccountModelSyncResult{AccountID: accountID}, err
	}
	return s.syncAccountModels(ctx, account), nil
}

func (s *AccountTestService) listAllAccounts(ctx context.Context) ([]Account, error) {
	const pageSize = 200
	all := make([]Account, 0, pageSize)
	for page := 1; ; page++ {
		accounts, result, err := s.accountRepo.List(ctx, pagination.PaginationParams{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, accounts...)
		if len(accounts) == 0 || result == nil || int64(len(all)) >= result.Total {
			return all, nil
		}
	}
}

func (s *AccountTestService) ListSyncedAccountModels(ctx context.Context) ([]AccountModelOption, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("account model sync is not configured")
	}
	accounts, err := s.listAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for i := range accounts {
		for _, model := range readAccountModelIDs(accounts[i].Extra[AccountAvailableModelsExtraKey]) {
			seen[model] = struct{}{}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	options := make([]AccountModelOption, 0, len(models))
	for _, model := range models {
		options = append(options, AccountModelOption{Value: model, Label: model})
	}
	return options, nil
}

func (s *AccountTestService) SyncAllAccountModels(ctx context.Context) (*AccountModelSyncSummary, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("account model sync is not configured")
	}
	accounts, err := s.listAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	summary := &AccountModelSyncSummary{Total: len(accounts), Results: make([]AccountModelSyncResult, len(accounts))}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range accounts {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				summary.Results[i] = AccountModelSyncResult{AccountID: accounts[i].ID, Status: accountModelsSyncFailed, Error: "request_canceled"}
				return
			}
			summary.Results[i] = s.syncAccountModels(ctx, &accounts[i])
		}()
	}
	wg.Wait()
	for _, result := range summary.Results {
		switch result.Status {
		case accountModelsSyncSuccess:
			summary.Success++
		case accountModelsSyncSkipped:
			summary.Unsupported++
		default:
			summary.Failed++
		}
	}
	return summary, nil
}
