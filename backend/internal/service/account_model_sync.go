package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const (
	accountModelsSyncedAtKey     = "available_models_synced_at"
	accountModelsSyncStatusKey   = "available_models_sync_status"
	accountModelsSyncErrorKey    = "available_models_sync_error"
	accountModelsSyncTimeout     = 20 * time.Second
	accountModelsSyncMaxResponse = 2 * 1024 * 1024
	accountModelsSyncSuccess     = "upstream"
	accountModelsSyncFallback    = "fallback"
	accountModelsSyncUnsupported = "unsupported"
	accountModelsSyncFailed      = "failed"
)

// AccountModelOption is a model identifier observed in an account's persisted
// upstream snapshot. It is intentionally limited to display-safe fields.
type AccountModelOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AccountModelSyncResult is the non-sensitive result of one model discovery.
// It intentionally contains no upstream URL, credential, or response body.
type AccountModelSyncResult struct {
	AccountID  int64    `json:"account_id"`
	Status     string   `json:"status"`
	ModelCount int      `json:"model_count"`
	Models     []string `json:"models,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// AccountModelSyncSummary aggregates a best-effort full-account model sync.
type AccountModelSyncSummary struct {
	Total       int                      `json:"total"`
	Success     int                      `json:"success"`
	Fallback    int                      `json:"fallback"`
	Failed      int                      `json:"failed"`
	Unsupported int                      `json:"unsupported"`
	Results     []AccountModelSyncResult `json:"results"`
}

// AccountModelSyncPreviewEntry describes a side-effect-free live upstream
// model comparison for one account. Version is used for optimistic apply.
type AccountModelSyncPreviewEntry struct {
	AccountID      int64    `json:"account_id"`
	AccountName    string   `json:"account_name"`
	Status         string   `json:"status"`
	Version        string   `json:"version"`
	CurrentModels  []string `json:"current_models,omitempty"`
	UpstreamModels []string `json:"upstream_models,omitempty"`
	Added          []string `json:"added,omitempty"`
	Removed        []string `json:"removed,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type AccountModelSyncPreview struct {
	Total   int                            `json:"total"`
	Changed int                            `json:"changed"`
	Results []AccountModelSyncPreviewEntry `json:"results"`
}

type AccountModelSyncApplyItem struct {
	AccountID int64    `json:"account_id" binding:"required"`
	Version   string   `json:"version" binding:"required"`
	Models    []string `json:"models" binding:"required,min=1"`
	// Mode controls automatic identity mappings only. Manual aliases and
	// wildcards are always preserved.
	Mode string `json:"mode,omitempty"`
}

type AccountModelSyncApplyResult struct {
	AccountID int64  `json:"account_id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

var ErrAccountModelSyncConflict = errors.New("account changed after model preview")

type accountModelSyncCredentialWriter interface {
	UpdateCredentialsIfUnchanged(context.Context, int64, map[string]any, time.Time) error
}

type accountModelSyncExtraWriter interface {
	UpdateExtra(context.Context, int64, map[string]any) error
}

type modelDiscoveryError struct {
	statusCode int
	message    string
}

func (e *modelDiscoveryError) Error() string {
	if e == nil {
		return "model discovery failed"
	}
	return e.message
}

// SyncAccountModels discovers and persists the model snapshot for one account.
// API-compatible accounts use their provider's model-list endpoint. Account
// types without a reliable list endpoint fall back to the configured mapping
// or built-in catalog and expose that source through the result status.
func (s *AccountTestService) SyncAccountModels(ctx context.Context, accountID int64) (AccountModelSyncResult, error) {
	result := AccountModelSyncResult{AccountID: accountID}
	if s == nil || s.accountRepo == nil {
		return result, errors.New("account model sync is not configured")
	}

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return result, err
	}

	previous := readAccountModelIDs(account.Extra[AccountAvailableModelsExtraKey])
	modelIDs, discoverErr := s.discoverAccountModels(ctx, account)
	status := accountModelsSyncSuccess
	if discoverErr != nil || len(modelIDs) == 0 {
		// A failed refresh must never replace a known-good upstream snapshot
		// with a broader platform default or an incomplete mapping.
		switch {
		case len(previous) > 0 && discoverErr != nil:
			modelIDs = previous
			status = accountModelsSyncFailed
		case len(configuredAccountModelIDs(account)) > 0:
			modelIDs = configuredAccountModelIDs(account)
			status = accountModelsSyncFallback
		case discoverErr != nil:
			status = accountModelsSyncFailed
		default:
			status = accountModelsSyncUnsupported
		}
	}

	updates := map[string]any{
		accountModelsSyncedAtKey:   time.Now().UTC().Format(time.RFC3339),
		accountModelsSyncStatusKey: status,
		accountModelsSyncErrorKey:  modelSyncErrorSummary(discoverErr),
	}
	if len(modelIDs) > 0 {
		updates[AccountAvailableModelsExtraKey] = modelIDs
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
		return result, err
	}

	result.Status = status
	result.ModelCount = len(modelIDs)
	if status == accountModelsSyncSuccess || status == accountModelsSyncFallback {
		result.Models = modelIDs
	}
	if discoverErr != nil {
		result.Error = modelSyncErrorSummary(discoverErr)
	}
	return result, nil
}

// ListSyncedAccountModels returns the distinct persisted model identifiers.
// It never performs upstream requests and is therefore safe for list filters.
func (s *AccountTestService) ListSyncedAccountModels(ctx context.Context) ([]AccountModelOption, error) {
	accounts, err := s.listAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for i := range accounts {
		for _, modelID := range readAccountModelIDs(accounts[i].Extra[AccountAvailableModelsExtraKey]) {
			seen[modelID] = struct{}{}
		}
	}
	models := make([]string, 0, len(seen))
	for modelID := range seen {
		models = append(models, modelID)
	}
	sort.Strings(models)
	options := make([]AccountModelOption, 0, len(models))
	for _, modelID := range models {
		options = append(options, AccountModelOption{Value: modelID, Label: modelID})
	}
	return options, nil
}

// SyncAllAccountModels refreshes all account snapshots with bounded
// concurrency. One unavailable upstream does not interrupt other accounts.
func (s *AccountTestService) SyncAllAccountModels(ctx context.Context) (*AccountModelSyncSummary, error) {
	accounts, err := s.listAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	summary := &AccountModelSyncSummary{
		Total:   len(accounts),
		Results: make([]AccountModelSyncResult, len(accounts)),
	}
	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i := range accounts {
		index := i
		accountID := accounts[i].ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				summary.Results[index] = AccountModelSyncResult{AccountID: accountID, Status: accountModelsSyncFailed, Error: "request_canceled"}
				return
			}
			result, syncErr := s.SyncAccountModels(ctx, accountID)
			if syncErr != nil {
				result = AccountModelSyncResult{AccountID: accountID, Status: accountModelsSyncFailed, Error: "sync_failed"}
			}
			summary.Results[index] = result
		}()
	}
	wg.Wait()
	for _, result := range summary.Results {
		switch result.Status {
		case accountModelsSyncSuccess:
			summary.Success++
		case accountModelsSyncFallback:
			summary.Fallback++
		case accountModelsSyncUnsupported:
			summary.Unsupported++
		default:
			summary.Failed++
		}
	}
	return summary, nil
}

// PreviewAllAccountModelMappings fetches live upstream models without writing
// snapshots or credentials. Existing exact identity mappings are compared;
// wildcard and translated mappings are intentionally excluded from removal.
func (s *AccountTestService) PreviewAllAccountModelMappings(ctx context.Context) (*AccountModelSyncPreview, error) {
	accounts, err := s.listAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	preview := &AccountModelSyncPreview{Total: len(accounts), Results: make([]AccountModelSyncPreviewEntry, len(accounts))}
	const workerCount = 5
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := workerCount
	if len(accounts) < workers {
		workers = len(accounts)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				account := &accounts[i]
				entry := AccountModelSyncPreviewEntry{AccountID: account.ID, AccountName: account.Name, Version: account.UpdatedAt.UTC().Format(time.RFC3339Nano)}
				entry.CurrentModels = exactMappedModels(account.Credentials)
				accountCtx, cancel := context.WithTimeout(ctx, accountModelsSyncTimeout)
				models, fetchErr := s.FetchUpstreamSupportedModels(accountCtx, account)
				cancel()
				if fetchErr != nil {
					entry.Status = accountModelsSyncFailed
					entry.Error = "upstream model discovery failed"
				} else {
					entry.Status = accountModelsSyncSuccess
					entry.UpstreamModels = models
					entry.Added, entry.Removed = modelSetDiff(entry.CurrentModels, models)
				}
				preview.Results[i] = entry
			}
		}()
	}
	for i := range accounts {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, entry := range preview.Results {
		if len(entry.Added) > 0 || len(entry.Removed) > 0 {
			preview.Changed++
		}
	}
	return preview, nil
}

// ApplyAccountModelMappings applies only selected accounts whose version still
// matches preview. Credentials are merged against the latest row, so secrets
// and non-model settings cannot be overwritten by stale preview data.
func (s *AccountTestService) ApplyAccountModelMappings(ctx context.Context, items []AccountModelSyncApplyItem) []AccountModelSyncApplyResult {
	results := make([]AccountModelSyncApplyResult, 0, len(items))
	for _, item := range items {
		result := AccountModelSyncApplyResult{AccountID: item.AccountID}
		account, err := s.accountRepo.GetByID(ctx, item.AccountID)
		if err != nil {
			result.Status, result.Error = "failed", "account not found"
			results = append(results, result)
			continue
		}
		version := account.UpdatedAt.UTC().Format(time.RFC3339Nano)
		if item.Version == "" || item.Version != version {
			result.Status, result.Error = "conflict", "account changed after preview"
			results = append(results, result)
			continue
		}
		models := uniqueModelIDs(item.Models)
		if len(models) == 0 {
			result.Status, result.Error = "failed", "upstream model discovery failed"
			results = append(results, result)
			continue
		}
		mode := item.Mode
		if mode == "" {
			mode = "add"
		}
		if mode != "add" && mode != "sync" {
			result.Status, result.Error = "failed", "invalid synchronization mode"
			results = append(results, result)
			continue
		}
		credentials := shallowCopyMap(account.Credentials)
		mapping := map[string]any{}
		upstream := make(map[string]struct{}, len(models))
		for _, model := range models {
			upstream[model] = struct{}{}
		}
		if raw, ok := credentials["model_mapping"].(map[string]any); ok {
			for k, v := range raw {
				ks, vs := strings.TrimSpace(k), strings.TrimSpace(fmt.Sprint(v))
				if ks != "" && vs != "" && (ks != vs || strings.Contains(ks, "*") || mode != "sync") {
					// In sync mode, remove stale automatically generated whitelist
					// entries, while retaining aliases and wildcard rules.
					if mode == "sync" && ks == vs && !strings.Contains(ks, "*") {
						if _, ok := upstream[ks]; !ok {
							continue
						}
					}
					mapping[ks] = v
				}
			}
		} else if raw, ok := credentials["model_mapping"].(map[string]string); ok {
			for k, v := range raw {
				ks, vs := strings.TrimSpace(k), strings.TrimSpace(v)
				if ks != "" && vs != "" && (ks != vs || strings.Contains(ks, "*") || mode != "sync") {
					mapping[ks] = v
				}
			}
		}
		for _, model := range models {
			if _, manual := mapping[model]; !manual {
				mapping[model] = model
			}
		}
		credentials["model_mapping"] = mapping
		writer, ok := s.accountRepo.(accountModelSyncCredentialWriter)
		if !ok {
			result.Status, result.Error = "failed", "conditional model update unavailable"
			results = append(results, result)
			continue
		}
		if err := writer.UpdateCredentialsIfUnchanged(ctx, account.ID, credentials, account.UpdatedAt); errors.Is(err, ErrAccountModelSyncConflict) {
			result.Status, result.Error = "conflict", "account changed after preview"
		} else if err != nil {
			result.Status, result.Error = "failed", "failed to persist model mapping"
		} else {
			result.Status = "applied"
			if extraWriter, ok := s.accountRepo.(accountModelSyncExtraWriter); ok {
				if err := extraWriter.UpdateExtra(ctx, account.ID, map[string]any{AccountAvailableModelsExtraKey: models, accountModelsSyncedAtKey: time.Now().UTC().Format(time.RFC3339), accountModelsSyncStatusKey: accountModelsSyncSuccess, accountModelsSyncErrorKey: ""}); err != nil {
					result.Status, result.Error = "failed", "failed to persist model snapshot"
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func exactMappedModels(credentials map[string]any) []string {
	result := []string{}
	raw := map[string]any{}
	switch mapping := credentials["model_mapping"].(type) {
	case map[string]any:
		raw = mapping
	case map[string]string:
		for key, value := range mapping {
			raw[key] = value
		}
	}
	for key, value := range raw {
		if strings.TrimSpace(key) != "" && key == strings.TrimSpace(fmt.Sprint(value)) && !strings.Contains(key, "*") {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func modelSetDiff(current, upstream []string) (added, removed []string) {
	cur, next := map[string]struct{}{}, map[string]struct{}{}
	for _, m := range current {
		cur[m] = struct{}{}
	}
	for _, m := range upstream {
		next[m] = struct{}{}
	}
	for m := range next {
		if _, ok := cur[m]; !ok {
			added = append(added, m)
		}
	}
	for m := range cur {
		if _, ok := next[m]; !ok {
			removed = append(removed, m)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func (s *AccountTestService) listAllAccounts(ctx context.Context) ([]Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("account model sync is not configured")
	}
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

func (s *AccountTestService) discoverAccountModels(ctx context.Context, account *Account) ([]string, error) {
	endpoint, authHeader, authValue, err := buildAccountModelDiscoveryRequest(account)
	if err != nil {
		return nil, err
	}
	if s.cfg == nil {
		return nil, errors.New("config is not available")
	}
	endpoint, err = s.validateUpstreamBaseURL(endpoint)
	if err != nil {
		return nil, errors.New("upstream model discovery URL is not allowed")
	}

	requestCtx, cancel := context.WithTimeout(ctx, accountModelsSyncTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid model discovery request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sub2api-model-sync/1.0")
	if account.Platform == PlatformAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if authHeader != "" {
		req.Header.Set(authHeader, authValue)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var profile *tlsfingerprint.Profile
	if s.tlsFPProfileService != nil {
		profile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.doModelDiscoveryRequest(req, proxyURL, account, profile)
	if err != nil {
		return nil, errors.New("upstream model discovery request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &modelDiscoveryError{statusCode: resp.StatusCode, message: fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, accountModelsSyncMaxResponse+1))
	if err != nil {
		return nil, errors.New("failed to read model discovery response")
	}
	if len(body) > accountModelsSyncMaxResponse {
		return nil, errors.New("model discovery response is too large")
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("invalid model discovery response")
	}
	return extractModelIDs(payload), nil
}

// doModelDiscoveryRequest keeps TLS profile resolution inside the existing
// HTTPUpstream abstraction. The type switch avoids changing that interface
// just for this administrative, low-frequency operation.
func (s *AccountTestService) doModelDiscoveryRequest(req *http.Request, proxyURL string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if s.httpUpstream == nil {
		return nil, errors.New("upstream client is not configured")
	}
	if profile != nil {
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, profile)
	}
	return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
}

func buildAccountModelDiscoveryRequest(account *Account) (string, string, string, error) {
	if account == nil {
		return "", "", "", errors.New("account is nil")
	}
	var baseURL string
	switch {
	case account.IsOpenAI():
		baseURL = account.GetOpenAIBaseURL()
	case account.IsAnthropic():
		baseURL = account.GetCredential("base_url")
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
	case account.IsGemini():
		baseURL = account.GetGeminiBaseURL("https://generativelanguage.googleapis.com")
	default:
		return "", "", "", errors.New("account type does not expose a model list endpoint")
	}

	endpoint, err := appendModelListPath(baseURL, account.Platform == PlatformGemini)
	if err != nil {
		return "", "", "", err
	}
	header, value := "", ""
	switch account.Platform {
	case PlatformOpenAI:
		value = account.GetOpenAIApiKey()
		if value == "" {
			value = account.GetOpenAIAccessToken()
		}
		if value != "" {
			header = "Authorization"
			value = "Bearer " + value
		}
	case PlatformAnthropic:
		value = account.GetCredential("api_key")
		if value != "" {
			header = "x-api-key"
		} else {
			value = account.GetCredential("access_token")
			if value != "" {
				header = "Authorization"
				value = "Bearer " + value
			}
		}
	case PlatformGemini:
		value = account.GetCredential("api_key")
		if value != "" {
			header = "x-goog-api-key"
		} else {
			value = account.GetCredential("access_token")
			if value != "" {
				header = "Authorization"
				value = "Bearer " + value
			}
		}
	}
	if value == "" {
		return "", "", "", errors.New("account has no model-list credential")
	}
	return endpoint, header, value, nil
}

func appendModelListPath(raw string, gemini bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid model-list base URL")
	}
	u.RawQuery = ""
	u.Fragment = ""
	basePath := strings.TrimRight(u.Path, "/")
	if gemini {
		if strings.HasSuffix(basePath, "/v1beta") || strings.HasSuffix(basePath, "/v1") {
			u.Path = basePath + "/models"
		} else {
			u.Path = basePath + "/v1beta/models"
		}
	} else if strings.HasSuffix(basePath, "/v1") {
		u.Path = basePath + "/models"
	} else {
		u.Path = basePath + "/v1/models"
	}
	return u.String(), nil
}

func extractModelIDs(payload any) []string {
	seen := make(map[string]struct{})
	var visit func(any)
	visit = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				visit(child)
			}
		case map[string]any:
			for _, key := range []string{"id", "name", "model"} {
				if raw, ok := item[key].(string); ok {
					if normalized := normalizeDiscoveredModelID(raw); normalized != "" {
						seen[normalized] = struct{}{}
					}
				}
			}
			for _, key := range []string{"data", "models", "results"} {
				if child, ok := item[key]; ok {
					visit(child)
				}
			}
		}
	}
	visit(payload)
	result := make([]string, 0, len(seen))
	for modelID := range seen {
		result = append(result, modelID)
	}
	sort.Strings(result)
	return result
}

func normalizeDiscoveredModelID(raw string) string {
	modelID := strings.TrimSpace(raw)
	modelID = strings.TrimPrefix(modelID, "models/")
	if modelID == "" || len(modelID) > 200 || strings.ContainsAny(modelID, "\r\n") {
		return ""
	}
	return modelID
}

func configuredAccountModelIDs(account *Account) []string {
	if account == nil {
		return nil
	}
	ids := make([]string, 0)
	for modelID := range account.GetModelMapping() {
		if normalized := normalizeDiscoveredModelID(modelID); normalized != "" {
			ids = append(ids, normalized)
		}
	}
	if len(ids) == 0 {
		switch account.Platform {
		case PlatformOpenAI:
			ids = append(ids, openai.DefaultModelIDs()...)
		case PlatformAnthropic:
			ids = append(ids, claude.DefaultModelIDs()...)
		case PlatformGemini:
			for _, model := range geminicli.DefaultModels {
				ids = append(ids, model.ID)
			}
		}
	}
	sort.Strings(ids)
	return uniqueModelIDs(ids)
}

func readAccountModelIDs(raw any) []string {
	var ids []string
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if modelID, ok := item.(string); ok {
				ids = append(ids, modelID)
			}
		}
	case []string:
		ids = append(ids, value...)
	}
	return uniqueModelIDs(ids)
}

func uniqueModelIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, raw := range ids {
		if modelID := normalizeDiscoveredModelID(raw); modelID != "" {
			if _, ok := seen[modelID]; !ok {
				seen[modelID] = struct{}{}
				result = append(result, modelID)
			}
		}
	}
	sort.Strings(result)
	return result
}

func modelSyncErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	var discoveryErr *modelDiscoveryError
	if errors.As(err, &discoveryErr) && discoveryErr.statusCode > 0 {
		return fmt.Sprintf("upstream_http_%d", discoveryErr.statusCode)
	}
	switch {
	case strings.Contains(err.Error(), "does not expose"):
		return "unsupported_model_list_endpoint"
	case strings.Contains(err.Error(), "no model-list credential"):
		return "missing_model_list_credential"
	case strings.Contains(err.Error(), "invalid model discovery response"):
		return "invalid_model_list_response"
	case strings.Contains(err.Error(), "too large"):
		return "model_list_response_too_large"
	default:
		return "model_discovery_failed"
	}
}
