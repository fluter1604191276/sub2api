package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// OpsResponseDiagnostic is metadata only. It must never contain prompts, output text, or credentials.
type OpsResponseDiagnostic struct {
	ID                       int64     `json:"id"`
	CreatedAt                time.Time `json:"created_at"`
	RequestID                string    `json:"request_id"`
	ClientRequestID          string    `json:"client_request_id"`
	UpstreamRequestID        string    `json:"upstream_request_id"`
	ResponseID               string    `json:"response_id"`
	Model                    string    `json:"model"`
	Status                   string    `json:"status"`
	Reason                   string    `json:"reason"`
	UserID                   *int64    `json:"user_id"`
	AccountID                *int64    `json:"account_id"`
	GroupID                  *int64    `json:"group_id"`
	Stream                   bool      `json:"stream"`
	ClientDisconnected       bool      `json:"client_disconnected"`
	HTTPStatus               int       `json:"http_status"`
	RequestedMaxOutputTokens *int64    `json:"requested_max_output_tokens"`
	ForwardedMaxOutputTokens *int64    `json:"forwarded_max_output_tokens"`
	UpstreamMaxOutputTokens  *int64    `json:"upstream_max_output_tokens"`
	InputTokens              *int64    `json:"input_tokens"`
	CacheReadTokens          *int64    `json:"cache_read_tokens"`
	CacheCreationTokens      *int64    `json:"cache_creation_tokens"`
	OutputTokens             *int64    `json:"output_tokens"`
	ReasoningTokens          *int64    `json:"reasoning_tokens"`
}
type OpsResponseDiagnosticFilter struct {
	StartTime, EndTime        *time.Time
	Status, Reason, RequestID string
	AccountID, UserID         *int64
	Page, PageSize            int
}
type OpsResponseDiagnosticList struct {
	Items    []*OpsResponseDiagnostic `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}
type OpsResponseDiagnosticRepository interface {
	InsertResponseDiagnostic(context.Context, *OpsResponseDiagnostic) error
	ListResponseDiagnostics(context.Context, *OpsResponseDiagnosticFilter) (*OpsResponseDiagnosticList, error)
}

func ValidResponseDiagnosticStatus(s string) bool {
	switch s {
	case "completed", "incomplete", "failed", "client_disconnected", "missing_terminal":
		return true
	}
	return false
}

// NormalizeResponseDiagnostic bounds metadata at the persistence boundary. Unknown reason codes
// stay useful, but free-form sentences are rejected rather than stored as possible response text.
func NormalizeResponseDiagnostic(d *OpsResponseDiagnostic) error {
	if d == nil {
		return fmt.Errorf("nil response diagnostic")
	}
	if !ValidResponseDiagnosticStatus(d.Status) {
		return fmt.Errorf("invalid response diagnostic status")
	}
	if strings.TrimSpace(d.RequestID) == "" {
		return fmt.Errorf("missing request id")
	}
	for _, v := range []*string{&d.RequestID, &d.ClientRequestID, &d.UpstreamRequestID, &d.ResponseID, &d.Model} {
		*v = strings.TrimSpace(*v)
		if len(*v) > 256 {
			*v = ""
			continue
		}
		for _, r := range *v {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.:/", r)) {
				*v = ""
				break
			}
		}
	}
	for _, id := range []**int64{&d.UserID, &d.AccountID, &d.GroupID} {
		if *id != nil && **id <= 0 {
			*id = nil
		}
	}

	if d.RequestID == "" {
		return fmt.Errorf("invalid request id")
	}
	if len(d.Reason) > 128 {
		d.Reason = "unknown"
	}
	for _, r := range d.Reason {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			d.Reason = "unknown"
			break
		}
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.RequestedMaxOutputTokens != nil && *d.RequestedMaxOutputTokens < 0 {
		d.RequestedMaxOutputTokens = nil
	}
	if d.ForwardedMaxOutputTokens != nil && *d.ForwardedMaxOutputTokens < 0 {
		d.ForwardedMaxOutputTokens = nil
	}
	if d.UpstreamMaxOutputTokens != nil && *d.UpstreamMaxOutputTokens < 0 {
		d.UpstreamMaxOutputTokens = nil
	}
	if d.InputTokens != nil && *d.InputTokens < 0 {
		d.InputTokens = nil
	}
	if d.CacheReadTokens != nil && *d.CacheReadTokens < 0 {
		d.CacheReadTokens = nil
	}
	if d.CacheCreationTokens != nil && *d.CacheCreationTokens < 0 {
		d.CacheCreationTokens = nil
	}
	if d.OutputTokens != nil && *d.OutputTokens < 0 {
		d.OutputTokens = nil
	}
	if d.ReasoningTokens != nil && *d.ReasoningTokens < 0 {
		d.ReasoningTokens = nil
	}
	return nil
}
func (s *OpsService) InsertResponseDiagnostic(ctx context.Context, d *OpsResponseDiagnostic) error {
	if s == nil || !s.IsMonitoringEnabled(ctx) {
		return nil
	}
	if d == nil {
		return fmt.Errorf("nil response diagnostic")
	}
	copy := *d
	if err := NormalizeResponseDiagnostic(&copy); err != nil {
		return err
	}
	r, ok := s.opsRepo.(OpsResponseDiagnosticRepository)
	if !ok {
		return fmt.Errorf("response diagnostics repository unavailable")
	}
	return r.InsertResponseDiagnostic(ctx, &copy)
}
func (s *OpsService) ListResponseDiagnostics(ctx context.Context, f *OpsResponseDiagnosticFilter) (*OpsResponseDiagnosticList, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	r, ok := s.opsRepo.(OpsResponseDiagnosticRepository)
	if !ok {
		return nil, fmt.Errorf("response diagnostics repository unavailable")
	}
	return r.ListResponseDiagnostics(ctx, f)
}
