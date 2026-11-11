package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const SettingKeyOpenAIIQ = "openai_iq"

// OpenAIIQConcurrency bounds the runner and its dedicated gateway identity.
const OpenAIIQConcurrency = 16

// Keep the diagnostic offset bounded even on 32-bit integer platforms.
const MaxIQDiagnosticsPage = 1_000_000
const MaxIQDiagnosticsPageSize = 50

const IQTriggerScheduled = "scheduled"
const IQTriggerManual = "manual"

var (
	ErrIQRunActive      = errors.New("an OpenAI IQ run is already active")
	ErrIQManualCooldown = errors.New("this group was manually rechecked within five minutes")
	ErrIQGroupInvalid   = errors.New("the OpenAI IQ group is not active or configured")
)

const defaultOpenAIIQQuestion = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。已知不同口味的糖果和不同形状的数量如下：

圆形：苹果味 6 颗，桃子味 6 颗，西瓜味 5 颗。
五角星形：苹果味 5 颗，桃子味 5 颗，西瓜味 3 颗。

参赛者需要在活动前决定摸出的糖果数目。最少取出多少个糖果，才能保证手中同时拥有不同形状的苹果味和桃子味的糖果？手中有圆形苹果味配五角星形桃子味，或者圆形桃子味配五角星形苹果味，都满足要求。取糖时允许在袋内凭手感选择形状，但不能预先辨别口味。请给出最少数量，并说明取法及为什么不能更少。`

type IQSettings struct {
	Enabled            bool    `json:"enabled"`
	Question           string  `json:"question"`
	ExpectedAnswer     string  `json:"expected_answer"`
	GroupIDs           []int64 `json:"group_ids"`
	Model              string  `json:"model"`
	IntervalSeconds    int     `json:"interval_seconds"`
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`
	PerRequestLimitUSD float64 `json:"per_request_limit_usd"`
	MaxOutputTokens    int     `json:"max_output_tokens"`
}

func DefaultIQSettings() IQSettings {
	return IQSettings{
		Question: defaultOpenAIIQQuestion, ExpectedAnswer: "15", Model: "gpt-6-astra",
		IntervalSeconds: 3600, DailyBudgetUSD: 1, PerRequestLimitUSD: 0.1,
		MaxOutputTokens: 2048, GroupIDs: []int64{},
	}
}

type IQGroup struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	RateMultiplier float64 `json:"rate_multiplier"`
}
type IQResult struct {
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	RateMultiplier float64    `json:"rate_multiplier"`
	Model          string     `json:"model"`
	ResponseModel  string     `json:"response_model,omitempty"`
	ExpectedAnswer string     `json:"expected_answer"`
	ModelAnswer    string     `json:"model_answer"`
	Output         string     `json:"output"`
	Status         string     `json:"status"`
	CheckedAt      *time.Time `json:"checked_at"`
	Error          string     `json:"error,omitempty"`
}
type IQSummary struct {
	Total     int `json:"total"`
	Correct   int `json:"correct"`
	Incorrect int `json:"incorrect"`
	Errors    int `json:"errors"`
	Pending   int `json:"pending"`
}
type IQView struct {
	Enabled         bool       `json:"enabled"`
	Model           string     `json:"model"`
	Question        string     `json:"question"`
	ExpectedAnswer  string     `json:"expected_answer"`
	IntervalSeconds int        `json:"interval_seconds"`
	RoundStartedAt  *time.Time `json:"round_started_at"`
	Results         []IQResult `json:"results"`
	Summary         IQSummary  `json:"summary"`
}
type IQBudget struct {
	Date             string     `json:"date"`
	SpentUSD         float64    `json:"spent_usd"`
	ReservedUSD      float64    `json:"reserved_usd"`
	RemainingUSD     float64    `json:"remaining_usd"`
	UnconfirmedUSD   float64    `json:"unconfirmed_usd"`
	UnconfirmedSince *time.Time `json:"unconfirmed_since"`
}

// IQPublicResult is deliberately separate from IQResult. IQResult contains
// operator diagnostics and must never be serialized from the user endpoint.
type IQPublicResult struct {
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	RateMultiplier float64    `json:"rate_multiplier"`
	Model          string     `json:"model"`
	Question       string     `json:"question"`
	ExpectedAnswer string     `json:"expected_answer"`
	ModelAnswer    string     `json:"model_answer"`
	Output         string     `json:"output"`
	Status         string     `json:"status"`
	CheckedAt      *time.Time `json:"checked_at"`
}
type IQPublicSummary struct {
	Total     int `json:"total"`
	Correct   int `json:"correct"`
	Incorrect int `json:"incorrect"`
	NoResults int `json:"no_results"`
}
type IQPublicView struct {
	Enabled         bool             `json:"enabled"`
	Model           string           `json:"model"`
	Question        string           `json:"question"`
	ExpectedAnswer  string           `json:"expected_answer"`
	IntervalSeconds int              `json:"interval_seconds"`
	Results         []IQPublicResult `json:"results"`
	Summary         IQPublicSummary  `json:"summary"`
}
type IQDiagnosticReservation struct {
	ReservedUSD   float64    `json:"reserved_usd"`
	ActualCostUSD *float64   `json:"actual_cost_usd"`
	RequestID     *string    `json:"request_id"`
	UsageLogID    *int64     `json:"usage_log_id"`
	AccountID     *int64     `json:"account_id"`
	SettledAt     *time.Time `json:"settled_at"`
}
type IQDiagnosticResult struct {
	ID    int64 `json:"id"`
	RunID int64 `json:"run_id"`
	IQResult
	Reservation *IQDiagnosticReservation `json:"reservation"`
}
type IQDiagnosticRun struct {
	ID             int64                `json:"id"`
	Trigger        string               `json:"trigger"`
	RequestedBy    *int64               `json:"requested_by"`
	LeaseActive    bool                 `json:"lease_active"`
	StartedAt      time.Time            `json:"started_at"`
	FinishedAt     *time.Time           `json:"finished_at"`
	Question       string               `json:"question"`
	ExpectedAnswer string               `json:"expected_answer"`
	Model          string               `json:"model"`
	Status         string               `json:"status"`
	Results        []IQDiagnosticResult `json:"results"`
}
type IQDiagnosticsPage struct {
	Runs     []IQDiagnosticRun `json:"runs"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type IQRun struct {
	ID        int64
	Trigger   string
	StartedAt time.Time
	Question  string
	Answer    string
	Model     string
}

type IQIdentity struct {
	UserID  int64
	Balance float64
}

type OpenAIIQRepository interface {
	Latest(context.Context, []int64) (*IQView, error)
	LatestValid(context.Context, []int64, []int64, string, string, string) ([]IQPublicResult, error)
	Diagnostics(context.Context, int, int) (*IQDiagnosticsPage, error)
	Budget(context.Context, float64) (IQBudget, error)
	ClaimDue(context.Context, IQSettings) (*IQRun, error)
	ReserveBudget(context.Context, int64, int64, time.Time, float64, float64) (bool, error)
	BindBudgetRequest(context.Context, int64, int64, int64, string) error
	ReconcileBudget(context.Context) error
	EnsureIdentity(context.Context, []int64) (*IQIdentity, error)
	Identity(context.Context) (*IQIdentity, error)
	GroupKey(context.Context, int64, int64) (*APIKey, error)
	RunResults(context.Context, int64) ([]IQResult, error)
	CompleteResult(context.Context, int64, IQResult) error
	FinishRun(context.Context, int64) error
	SaveSettings(context.Context, IQSettings) error
}

// IQManualRunRepository claims manual and scheduled runs under the same
// cross-instance lock. It is separate so existing read-only adapters can
// continue to implement OpenAIIQRepository without supporting manual runs.
type IQManualRunRepository interface {
	ClaimManual(context.Context, IQSettings, int64, int64) (*IQRun, error)
}

type OpenAIIQService struct {
	settings     SettingRepository
	groups       GroupRepository
	users        UserRepository
	apiKeys      *APIKeyService
	repo         OpenAIIQRepository
	mu           sync.Mutex
	billing      *BillingService
	channels     *ChannelService
	accounts     AccountRepository
	cfg          *config.Config
	client       iqHTTPClient
	endpoint     string
	requestToken []byte
	startOnce    sync.Once
	cancel       context.CancelFunc
	ctx          context.Context
	done         chan struct{}
	lifecycleMu  sync.Mutex
	stopped      bool
	manualWG     sync.WaitGroup
}

func NewOpenAIIQService(settings SettingRepository, groups GroupRepository, users UserRepository, apiKeys *APIKeyService, repo OpenAIIQRepository, billing *BillingService, channels *ChannelService, accounts AccountRepository, cfg *config.Config) *OpenAIIQService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &OpenAIIQService{
		settings: settings, groups: groups, users: users, apiKeys: apiKeys, repo: repo,
		billing: billing, channels: channels, accounts: accounts, cfg: cfg,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	s.configureIQHTTP()
	return s
}
func (s *OpenAIIQService) Settings(ctx context.Context) (IQSettings, error) {
	out := DefaultIQSettings()
	if s == nil || s.settings == nil {
		return out, nil
	}
	raw, err := s.settings.GetValue(ctx, SettingKeyOpenAIIQ)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return out, nil
		}
		return out, err
	}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("parse OpenAI IQ settings: %w", err)
	}
	return normalizeIQSettings(out)
}
func (s *OpenAIIQService) UpdateSettings(ctx context.Context, in IQSettings) (IQSettings, error) {
	out, err := normalizeIQSettings(in)
	if err != nil {
		return out, infraerrors.BadRequest("OPENAI_IQ_SETTINGS_INVALID", err.Error())
	}
	groups, err := s.groups.ListActiveByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return out, err
	}
	available := map[int64]bool{}
	for _, g := range groups {
		available[g.ID] = true
	}
	for _, id := range out.GroupIDs {
		if out.Enabled && !available[id] {
			return out, infraerrors.BadRequest("OPENAI_IQ_GROUP_INVALID", fmt.Sprintf("OpenAI group %d is not active", id))
		}
	}
	if err = s.repo.SaveSettings(ctx, out); err != nil {
		return out, err
	}
	return out, nil
}
func normalizeIQSettings(in IQSettings) (IQSettings, error) {
	in.Question = strings.TrimSpace(in.Question)
	in.ExpectedAnswer = strings.TrimSpace(in.ExpectedAnswer)
	in.Model = strings.TrimSpace(in.Model)
	if in.Question == "" || in.ExpectedAnswer == "" || in.Model == "" {
		return in, fmt.Errorf("question, expected_answer and model are required")
	}
	if strings.ContainsAny(in.ExpectedAnswer, "\r\n") {
		return in, fmt.Errorf("expected_answer must be a single line")
	}
	if len(in.GroupIDs) > 64 || (in.Enabled && len(in.GroupIDs) == 0) {
		return in, fmt.Errorf("select between 1 and 64 groups when enabled")
	}
	if len(in.Question) > 16000 || len(in.ExpectedAnswer) > 256 || len(in.Model) > 200 {
		return in, fmt.Errorf("OpenAI IQ setting is too large")
	}
	if in.IntervalSeconds < 3600 || in.IntervalSeconds > 86400 {
		return in, fmt.Errorf("interval_seconds must be between 3600 and 86400")
	}
	if !finitePositive(in.DailyBudgetUSD) || in.DailyBudgetUSD > 1000 {
		return in, fmt.Errorf("daily_budget_usd must be between 0 and 1000")
	}
	if !finitePositive(in.PerRequestLimitUSD) || in.PerRequestLimitUSD > in.DailyBudgetUSD {
		return in, fmt.Errorf("per_request_limit_usd must be positive and no more than daily_budget_usd")
	}
	if in.MaxOutputTokens < 32 || in.MaxOutputTokens > 4096 {
		return in, fmt.Errorf("max_output_tokens must be between 32 and 4096")
	}
	seen := map[int64]bool{}
	for _, id := range in.GroupIDs {
		if id <= 0 || seen[id] {
			return in, fmt.Errorf("group_ids must be positive and unique")
		}
		seen[id] = true
	}
	sort.Slice(in.GroupIDs, func(i, j int) bool { return in.GroupIDs[i] < in.GroupIDs[j] })
	return in, nil
}
func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func (s *OpenAIIQService) Groups(ctx context.Context) ([]IQGroup, error) {
	gs, err := s.groups.ListActiveByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	out := make([]IQGroup, 0, len(gs))
	for _, g := range gs {
		out = append(out, IQGroup{g.ID, g.Name, g.RateMultiplier})
	}
	return out, nil
}
func (s *OpenAIIQService) View(ctx context.Context, userID int64) (*IQView, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var allowed []int64
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var available []Group
	if user.Role == RoleAdmin {
		available, err = s.groups.ListActiveByPlatform(ctx, PlatformOpenAI)
	} else if s.apiKeys != nil {
		available, err = s.apiKeys.GetAvailableGroups(ctx, user.ID)
	} else {
		return nil, errors.New("OpenAI IQ group access service is unavailable")
	}
	if err != nil {
		return nil, err
	}
	for _, group := range available {
		if group.Platform == PlatformOpenAI {
			allowed = append(allowed, group.ID)
		}
	}
	view, err := s.repo.Latest(ctx, allowed)
	if err != nil {
		return nil, err
	}
	if view == nil {
		view = &IQView{Results: []IQResult{}}
	}
	view.Enabled = settings.Enabled
	if view.RoundStartedAt == nil {
		view.Model = settings.Model
		view.Question = settings.Question
		view.ExpectedAnswer = settings.ExpectedAnswer
	}
	view.IntervalSeconds = settings.IntervalSeconds
	if view.Results == nil {
		view.Results = []IQResult{}
	}
	view.Summary = summarizeIQResults(view.Results)
	return view, nil
}

func (s *OpenAIIQService) PublicView(ctx context.Context, userID int64) (*IQPublicView, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	var available []Group
	if user.Role == RoleAdmin {
		available, err = s.groups.ListActiveByPlatform(ctx, PlatformOpenAI)
	} else if s.apiKeys != nil {
		available, err = s.apiKeys.GetAvailableGroups(ctx, user.ID)
	} else {
		return nil, errors.New("OpenAI IQ group access service is unavailable")
	}
	if err != nil {
		return nil, err
	}
	allowed := make([]int64, 0, len(available))
	for _, group := range available {
		if group.Platform == PlatformOpenAI {
			allowed = append(allowed, group.ID)
		}
	}
	out := &IQPublicView{Enabled: settings.Enabled, Model: settings.Model, Question: settings.Question, ExpectedAnswer: settings.ExpectedAnswer, IntervalSeconds: settings.IntervalSeconds, Results: []IQPublicResult{}}
	out.Results, err = s.repo.LatestValid(ctx, allowed, settings.GroupIDs, settings.Model, settings.Question, settings.ExpectedAnswer)
	if err != nil {
		return nil, err
	}
	if out.Results == nil {
		out.Results = []IQPublicResult{}
	}
	for _, result := range out.Results {
		out.Summary.Total++
		switch result.Status {
		case "correct":
			out.Summary.Correct++
		case "incorrect":
			out.Summary.Incorrect++
		default:
			out.Summary.NoResults++
		}
	}
	return out, nil
}
func (s *OpenAIIQService) Diagnostics(ctx context.Context, page, pageSize int) (*IQDiagnosticsPage, error) {
	if page > MaxIQDiagnosticsPage {
		return nil, infraerrors.BadRequest("OPENAI_IQ_PAGE_INVALID", fmt.Sprintf("page must not exceed %d", MaxIQDiagnosticsPage))
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxIQDiagnosticsPageSize {
		pageSize = MaxIQDiagnosticsPageSize
	}
	return s.repo.Diagnostics(ctx, page, pageSize)
}

func summarizeIQResults(results []IQResult) IQSummary {
	summary := IQSummary{Total: len(results)}
	for _, result := range results {
		switch result.Status {
		case "correct":
			summary.Correct++
		case "incorrect":
			summary.Incorrect++
		case "pending":
			summary.Pending++
		default:
			summary.Errors++
		}
	}
	return summary
}
func (s *OpenAIIQService) Admin(ctx context.Context) (IQSettings, []IQGroup, IQBudget, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return set, nil, IQBudget{}, err
	}
	gs, err := s.Groups(ctx)
	if err != nil {
		return set, nil, IQBudget{}, err
	}
	budget, err := s.repo.Budget(ctx, set.DailyBudgetUSD)
	return set, gs, budget, err
}

func (s *OpenAIIQService) TestIdentity(ctx context.Context) (*IQIdentity, error) {
	return s.repo.Identity(ctx)
}

func ExtractIQAnswer(output string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 {
		return "", false
	}
	const prefix = "FINAL_ANSWER:"
	line := strings.TrimSpace(lines[last])
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	answer := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if answer == "" {
		return "", false
	}
	for _, prior := range lines[:last] {
		if strings.HasPrefix(strings.TrimSpace(prior), prefix) {
			return "", false
		}
	}
	return answer, true
}
func IQAnswerCorrect(output, expected string) bool {
	answer, ok := ExtractIQAnswer(output)
	if !ok {
		return false
	}
	expected = strings.TrimSpace(expected)
	if strings.EqualFold(answer, expected) {
		return true
	}
	if _, err := strconv.ParseInt(expected, 10, 64); err != nil {
		return false
	}
	for _, unit := range []string{"颗", "个"} {
		if strings.HasSuffix(answer, unit) {
			return strings.TrimSpace(strings.TrimSuffix(answer, unit)) == expected
		}
	}
	return false
}
func IQBudgetDate() time.Time { return timezone.Today() }
