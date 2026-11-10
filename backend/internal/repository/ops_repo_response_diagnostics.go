package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const responseDiagnosticColumns = "id,created_at,request_id,turn_id,client_request_id,upstream_request_id,response_id,model,status,reason,user_id,account_id,group_id,stream,client_disconnected,http_status,requested_max_output_tokens,forwarded_max_output_tokens,upstream_max_output_tokens,input_tokens,cache_read_tokens,cache_creation_tokens,output_tokens,reasoning_tokens"

func (r *opsRepository) InsertResponseDiagnostic(ctx context.Context, d *service.OpsResponseDiagnostic) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if d == nil {
		return fmt.Errorf("nil response diagnostic")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO ops_response_diagnostics (created_at,request_id,turn_id,client_request_id,upstream_request_id,response_id,model,status,reason,user_id,account_id,group_id,stream,client_disconnected,http_status,requested_max_output_tokens,forwarded_max_output_tokens,upstream_max_output_tokens,input_tokens,cache_read_tokens,cache_creation_tokens,output_tokens,reasoning_tokens) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) ON CONFLICT (request_id,turn_id) DO NOTHING`, d.CreatedAt, d.RequestID, d.TurnID, d.ClientRequestID, d.UpstreamRequestID, d.ResponseID, d.Model, d.Status, d.Reason, d.UserID, d.AccountID, d.GroupID, d.Stream, d.ClientDisconnected, d.HTTPStatus, d.RequestedMaxOutputTokens, d.ForwardedMaxOutputTokens, d.UpstreamMaxOutputTokens, d.InputTokens, d.CacheReadTokens, d.CacheCreationTokens, d.OutputTokens, d.ReasoningTokens)
	return err
}
func (r *opsRepository) ListResponseDiagnostics(ctx context.Context, f *service.OpsResponseDiagnosticFilter) (*service.OpsResponseDiagnosticList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if f == nil {
		f = &service.OpsResponseDiagnosticFilter{}
	}
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if page > service.AdminListMaxPage {
		page = service.AdminListMaxPage
	}
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	end := time.Now()
	if f.EndTime != nil {
		end = *f.EndTime
	}
	start := end.Add(-time.Hour)
	if f.StartTime != nil {
		start = *f.StartTime
	}
	if start.After(end) || end.Sub(start) > 30*24*time.Hour {
		return nil, fmt.Errorf("invalid diagnostic time range")
	}
	args := []any{start, end}
	conditions := []string{"created_at >= $1", "created_at < $2"}
	add := func(col string, v any) {
		args = append(args, v)
		conditions = append(conditions, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.Status != "" {
		if !service.ValidResponseDiagnosticStatus(f.Status) {
			return nil, fmt.Errorf("invalid diagnostic status")
		}
		add("status", f.Status)
	}
	if f.Reason != "" {
		add("reason", f.Reason)
	}
	if f.UserID != nil {
		add("user_id", *f.UserID)
	}
	if f.AccountID != nil {
		add("account_id", *f.AccountID)
	}
	if f.RequestID != "" {
		args = append(args, f.RequestID)
		n := len(args)
		conditions = append(conditions, fmt.Sprintf("(request_id=$%d OR client_request_id=$%d OR upstream_request_id=$%d OR response_id=$%d)", n, n, n, n))
	}
	where := " WHERE " + strings.Join(conditions, " AND ")
	out := &service.OpsResponseDiagnosticList{Items: []*service.OpsResponseDiagnostic{}, Page: page, PageSize: size}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_response_diagnostics"+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	query := "SELECT " + responseDiagnosticColumns + " FROM ops_response_diagnostics" + where + fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, size, int64(page-1)*int64(size))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d := &service.OpsResponseDiagnostic{}
		if err := rows.Scan(&d.ID, &d.CreatedAt, &d.RequestID, &d.TurnID, &d.ClientRequestID, &d.UpstreamRequestID, &d.ResponseID, &d.Model, &d.Status, &d.Reason, &d.UserID, &d.AccountID, &d.GroupID, &d.Stream, &d.ClientDisconnected, &d.HTTPStatus, &d.RequestedMaxOutputTokens, &d.ForwardedMaxOutputTokens, &d.UpstreamMaxOutputTokens, &d.InputTokens, &d.CacheReadTokens, &d.CacheCreationTokens, &d.OutputTokens, &d.ReasoningTokens); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, d)
	}
	return out, rows.Err()
}
