package service

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

// AccountPriorityConversionRow exposes only the fields needed to confirm an
// administrator's manual conversion. It never includes account credentials.
type AccountPriorityConversionRow struct {
	AccountID      int64   `json:"account_id"`
	Name           string  `json:"name"`
	RateMultiplier *string `json:"rate_multiplier"`
	OldPriority    int     `json:"old_priority"`
	NewPriority    *int    `json:"new_priority"`
	Reason         string  `json:"reason,omitempty"`
	Applied        bool    `json:"applied"`
}

type AccountPriorityConfirmation struct {
	AccountID      int64  `json:"account_id" binding:"required,gt=0"`
	RateMultiplier string `json:"rate_multiplier" binding:"required"`
	OldPriority    int    `json:"old_priority"`
}

type AccountPriorityConversionResult struct {
	Rows    []AccountPriorityConversionRow `json:"rows"`
	Updated int                            `json:"updated"`
}

// Narrow extensions avoid making unrelated repository mocks implement this
// administrator-only operation. Production implementations are checked below.
type AccountPriorityConversionAdminService interface {
	PreviewAccountPriorityConversion(context.Context, []int64) (*AccountPriorityConversionResult, error)
	ApplyAccountPriorityConversion(context.Context, []AccountPriorityConfirmation) (*AccountPriorityConversionResult, error)
}

type AccountPriorityConversionRepository interface {
	GetPriorityConversionAccounts(context.Context, []int64) ([]AccountPriorityConversionRow, error)
	UpdatePriorityIfUnchanged(context.Context, int64, string, int, int) (bool, error)
}

var _ AccountPriorityConversionAdminService = (*adminServiceImpl)(nil)

func priorityFromAccountMultiplier(value *string) (*string, *int, string) {
	if value == nil {
		return nil, nil, "missing_multiplier"
	}
	s := strings.TrimSpace(*value)
	// Read numeric(10,4) as text, without passing through binary float arithmetic
	// or the billing fallback / shadow-account inheritance helpers.
	d, err := decimal.NewFromString(s)
	if err != nil {
		return &s, nil, "invalid_multiplier"
	}
	if d.IsNegative() {
		return &s, nil, "negative_multiplier"
	}
	n := d.Mul(decimal.NewFromInt(1000)).Ceil()
	if n.GreaterThan(decimal.NewFromInt(math.MaxInt32)) {
		return &s, nil, "priority_out_of_range"
	}
	priority := int(n.IntPart())
	if priority == 0 {
		priority = 1
	}
	return &s, &priority, ""
}

func (s *adminServiceImpl) PreviewAccountPriorityConversion(ctx context.Context, ids []int64) (*AccountPriorityConversionResult, error) {
	repo, ok := s.accountRepo.(AccountPriorityConversionRepository)
	if !ok {
		return nil, errors.New("priority conversion repository unavailable")
	}
	accounts, err := repo.GetPriorityConversionAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]AccountPriorityConversionRow, len(accounts))
	for _, account := range accounts {
		account.RateMultiplier, account.NewPriority, account.Reason = priorityFromAccountMultiplier(account.RateMultiplier)
		byID[account.AccountID] = account
	}
	result := &AccountPriorityConversionResult{Rows: make([]AccountPriorityConversionRow, 0, len(ids))}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			row, found := byID[id]
			if !found {
				row = AccountPriorityConversionRow{AccountID: id, Reason: "account_unavailable"}
			}
			result.Rows = append(result.Rows, row)
			seen[id] = true
		}
	}
	return result, nil
}

func (s *adminServiceImpl) ApplyAccountPriorityConversion(ctx context.Context, confirmations []AccountPriorityConfirmation) (*AccountPriorityConversionResult, error) {
	repo, ok := s.accountRepo.(AccountPriorityConversionRepository)
	if !ok {
		return nil, errors.New("priority conversion repository unavailable")
	}
	ids := make([]int64, 0, len(confirmations))
	for _, item := range confirmations {
		ids = append(ids, item.AccountID)
	}
	preview, err := s.PreviewAccountPriorityConversion(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]AccountPriorityConversionRow, len(preview.Rows))
	for _, row := range preview.Rows {
		byID[row.AccountID] = row
	}
	result := &AccountPriorityConversionResult{Rows: make([]AccountPriorityConversionRow, 0, len(preview.Rows))}
	seen := make(map[int64]bool, len(confirmations))
	for _, item := range confirmations {
		if seen[item.AccountID] {
			continue
		}
		seen[item.AccountID] = true
		row := byID[item.AccountID]
		if row.Reason == "" && (row.RateMultiplier == nil || *row.RateMultiplier != item.RateMultiplier || row.OldPriority != item.OldPriority) {
			row.Reason = "preview_changed"
		}
		if row.Reason == "" {
			applied, applyErr := repo.UpdatePriorityIfUnchanged(ctx, item.AccountID, item.RateMultiplier, item.OldPriority, *row.NewPriority)
			switch {
			case applyErr != nil:
				row.Reason = "update_failed"
			case !applied:
				row.Reason = "preview_changed"
			default:
				row.Applied = true
				result.Updated++
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}
