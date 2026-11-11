package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const iqRoundTimeout = 9 * time.Minute
const iqResultWriteTimeout = 5 * time.Second
const iqUsageGracePeriod = 5 * time.Second

const iqUnconfirmedBudgetReason = "费用未确认占用预算，已保留预留并结束本轮等待；迟到用量仍会按实结算"

type iqGroupJob struct {
	result        IQResult
	key           string
	body          []byte
	dispatchID    string
	waitForBudget bool
}

// ProvideOpenAIIQService starts the same instance used by the HTTP handlers.
func ProvideOpenAIIQService(settings SettingRepository, groups GroupRepository, users UserRepository, apiKeys *APIKeyService, repo OpenAIIQRepository, billing *BillingService, channels *ChannelService, accounts AccountRepository, cfg *config.Config) *OpenAIIQService {
	s := NewOpenAIIQService(settings, groups, users, apiKeys, repo, billing, channels, accounts, cfg)
	s.Start()
	return s
}

func (s *OpenAIIQService) Start() {
	s.startOnce.Do(func() {
		go func() {
			defer close(s.done)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					if err := s.runIQRound(s.ctx); err != nil {
						// Provider bodies and key-bearing errors are never logged.
						slog.Warn("OpenAI IQ round could not complete")
					}
				}
			}
		}()
	})
}

func (s *OpenAIIQService) Stop() {
	s.lifecycleMu.Lock()
	s.stopped = true
	s.cancel()
	s.lifecycleMu.Unlock()
	// A service constructed for a test can also be stopped before Start.
	s.startOnce.Do(func() { close(s.done) })
	<-s.done
	s.manualWG.Wait()
}

func (s *OpenAIIQService) runIQRound(parent context.Context) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	// Usage recording is asynchronous. Reconcile durable evidence even when
	// detection is disabled or an earlier round ended before its log arrived.
	if err := s.repo.ReconcileBudget(parent); err != nil {
		return err
	}
	settings, err := s.Settings(parent)
	if err != nil || !settings.Enabled {
		return err
	}
	identity, err := s.repo.Identity(parent)
	if err != nil {
		return err
	}
	if identity == nil || !finitePositive(identity.Balance) {
		return nil
	}
	// The worker's time budget starts before claiming the database lease. Lock,
	// commit, or connection delays therefore cannot extend execution beyond the
	// durable lease that starts during the claim.
	deadline := time.Now().Add(iqRoundTimeout)
	run, err := s.repo.ClaimDue(parent, settings)
	if err != nil || run == nil {
		return err
	}
	return s.executeIQRun(parent, run, settings, identity.UserID, deadline)
}

func (s *OpenAIIQService) executeIQRun(parent context.Context, run *IQRun, settings IQSettings, userID int64, deadline time.Time) error {
	// The durable lease is ten minutes. Cancellation ends all local requests
	// before it can expire, and a crashed process is never allowed to replay it.
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	defer func() {
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		if err := s.repo.FinishRun(finishCtx, run.ID); err != nil {
			slog.Warn("OpenAI IQ round finalization failed")
		}
	}()
	results, err := s.repo.RunResults(ctx, run.ID)
	if err != nil {
		return err
	}
	if s.apiKeys != nil {
		// The dedicated identity may have been upgraded from the serial runner.
		s.apiKeys.InvalidateAuthCacheByUserID(ctx, userID)
	}
	return s.runIQGroups(ctx, run, settings, userID, results)
}

// runIQGroups coordinates admission and reconciliation in one goroutine. Only
// reserved, durably bound requests enter the bounded HTTP workers.
func (s *OpenAIIQService) runIQGroups(ctx context.Context, run *IQRun, settings IQSettings, userID int64, pending []IQResult) error {
	var waiting []IQResult
	completed := make(chan struct{}, OpenAIIQConcurrency)
	active := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for len(pending) > 0 || len(waiting) > 0 || active > 0 {
		if reason := iqAdmissionStopReason(ctx); reason != "" {
			if ctx.Err() != nil {
				for active > 0 {
					<-completed
					active--
				}
			}
			s.skipIQResults(run.ID, append(pending, waiting...), reason)
			pending, waiting = nil, nil
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		for len(pending) > 0 && active < OpenAIIQConcurrency && canStartIQRequest(ctx) {
			result := pending[0]
			pending = pending[1:]
			job := s.prepareIQGroup(ctx, run, settings, userID, result)
			if job.waitForBudget {
				waiting = append(waiting, result)
				waiting = append(waiting, pending...)
				pending = nil
				break
			}
			if job.dispatchID == "" {
				s.persistIQResult(ctx, run.ID, job.result)
				continue
			}
			active++
			go func(job iqGroupJob) {
				result := s.executeIQGroup(ctx, run, job)
				writeCtx, cancel := context.WithTimeout(context.Background(), iqResultWriteTimeout)
				defer cancel()
				s.persistIQResult(writeCtx, run.ID, result)
				completed <- struct{}{}
			}(job)
		}
		if active == 0 && len(pending) == 0 && len(waiting) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			// Finalization must wait for every dispatched result write.
			for active > 0 {
				<-completed
				active--
			}
			s.skipIQResults(run.ID, append(pending, waiting...), iqAdmissionStopReason(ctx))
			return ctx.Err()
		case <-completed:
			active--
		case <-ticker.C:
		}
		if len(waiting) == 0 {
			if err := s.repo.ReconcileBudget(ctx); err != nil {
				slog.Warn("OpenAI IQ expense settlement deferred")
			}
			continue
		}
		current, err := s.Settings(ctx)
		if err != nil {
			slog.Warn("OpenAI IQ settings refresh deferred")
			continue
		}
		if !current.Enabled && run.Trigger != IQTriggerManual {
			s.skipIQResults(run.ID, append(pending, waiting...), "检测已关闭，停止本轮未发起的请求")
			pending, waiting = nil, nil
			continue
		}
		kept := waiting[:0]
		for _, result := range waiting {
			if slices.Contains(current.GroupIDs, result.GroupID) {
				kept = append(kept, result)
			} else {
				s.skipIQResults(run.ID, []IQResult{result}, "分组已从检测配置中移除")
			}
		}
		waiting = kept
		if len(waiting) == 0 {
			continue
		}
		dailyLimit := math.Min(settings.DailyBudgetUSD, current.DailyBudgetUSD)
		allowance := math.Min(settings.PerRequestLimitUSD, current.PerRequestLimitUSD)
		budget, err := s.repo.Budget(ctx, dailyLimit)
		if err != nil {
			slog.Warn("OpenAI IQ expense settlement deferred")
			continue
		}
		// Budget admission itself uses exact NUMERIC arithmetic in PostgreSQL.
		// Ignore float conversion noise below half its stored monetary unit here.
		const roundingTolerance = 0.00000000005
		if budget.RemainingUSD+roundingTolerance >= allowance {
			pending = append(pending, waiting...)
			waiting = nil
		} else if reason := iqBudgetStopReason(budget, dailyLimit, allowance, time.Now()); reason != "" {
			s.skipIQResults(run.ID, waiting, reason)
			waiting = nil
		}
	}
	return nil
}

func canStartIQRequest(ctx context.Context) bool {
	return iqAdmissionStopReason(ctx) == ""
}

func iqAdmissionStopReason(ctx context.Context) string {
	if ctx.Err() == context.Canceled {
		return "本轮执行已取消，未发起该分组请求"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "本轮执行已到时间上限，未发起该分组请求"
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < iqHTTPTimeout+iqResultWriteTimeout {
		return "本轮剩余时间不足，已停止发起新请求"
	}
	return ""
}

func iqBudgetStopReason(budget IQBudget, dailyLimit, allowance float64, now time.Time) string {
	const tolerance = 0.00000000005
	if budget.ReservedUSD <= tolerance || budget.SpentUSD+allowance > dailyLimit+tolerance {
		return "今日实际支出已达预算准入上限"
	}
	// Keep waiting while active requests could still settle and free enough
	// budget. Unconfirmed holds are never released on the strength of a timer.
	activeHolds := math.Max(0, budget.ReservedUSD-budget.UnconfirmedUSD)
	if budget.UnconfirmedUSD > 0 && budget.RemainingUSD+activeHolds+tolerance < allowance &&
		(budget.UnconfirmedSince == nil || now.Sub(*budget.UnconfirmedSince) >= iqUsageGracePeriod) {
		return iqUnconfirmedBudgetReason
	}
	return ""
}

func (s *OpenAIIQService) skipIQResults(runID int64, results []IQResult, reason string) {
	if len(results) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.Background(), iqResultWriteTimeout)
	defer cancel()
	for _, result := range results {
		result.Status, result.Error = "skipped", reason
		s.persistIQResult(writeCtx, runID, result)
	}
}

func (s *OpenAIIQService) persistIQResult(ctx context.Context, runID int64, result IQResult) {
	if err := s.repo.CompleteResult(ctx, runID, result); err != nil {
		slog.Warn("OpenAI IQ result persistence failed")
	}
}

func (s *OpenAIIQService) prepareIQGroup(ctx context.Context, run *IQRun, settings IQSettings, userID int64, original IQResult) iqGroupJob {
	job := iqGroupJob{result: original}
	result := &job.result
	result.Status = "skipped"
	current, err := s.Settings(ctx)
	if err != nil {
		result.Error = "无法读取检测设置，未发起该分组请求"
		return job
	}
	if reason := iqAdmissionStopReason(ctx); reason != "" {
		result.Error = reason
		return job
	}
	if !current.Enabled && run.Trigger != IQTriggerManual {
		result.Error = "检测已关闭，停止本轮未发起的请求"
		return job
	}
	if !slices.Contains(current.GroupIDs, result.GroupID) {
		result.Error = "分组已从检测配置中移除"
		return job
	}
	group, err := s.groups.GetByID(ctx, result.GroupID)
	if err != nil || group == nil || group.Platform != PlatformOpenAI || group.Status != StatusActive {
		result.Error = "检测分组当前不可用"
		return job
	}
	// These are ordinary gateway keys belonging only to the test identity.
	// Subscription groups still require that identity's own valid subscription.
	key, err := s.repo.GroupKey(ctx, userID, result.GroupID)
	if err != nil || key == nil {
		result.Error = "专用检测 Key 不可用，请检查测试账户和分组授权"
		return job
	}
	body, err := buildIQRequest(run, settings.MaxOutputTokens)
	if err != nil {
		result.Error = "检测请求配置无效"
		return job
	}
	limit := math.Min(settings.PerRequestLimitUSD, current.PerRequestLimitUSD)
	estimate, err := s.estimateIQCost(ctx, group, run.Model, body, settings.MaxOutputTokens)
	if err != nil || !isFiniteIQCost(estimate) || estimate > limit {
		result.Error = "费用无法可靠预估或超过单次预算，请检查模型价格和预算"
		return job
	}
	if !canStartIQRequest(ctx) {
		result.Error = iqAdmissionStopReason(ctx)
		return job
	}
	// Hold the full allowance until a matching gateway usage snapshot confirms
	// upstream expenditure. Unknown usage keeps its reservation across restarts.
	reserved, err := s.repo.ReserveBudget(ctx, run.ID, group.ID, IQBudgetDate(), limit, math.Min(settings.DailyBudgetUSD, current.DailyBudgetUSD))
	if err != nil || !reserved {
		result.Error = "预算暂不可准入，等待费用结算"
		job.waitForBudget = err == nil
		if err != nil {
			result.Error = "检测预算预留失败，未发起该分组请求"
		}
		return job
	}
	dispatchID := uuid.NewString()
	if err := s.repo.BindBudgetRequest(ctx, run.ID, group.ID, key.ID, "client:iq-"+dispatchID); err != nil {
		result.Error = "检测费用关联无法保存，已保留预算并停止本次请求"
		return job
	}
	job.key, job.body, job.dispatchID = key.Key, body, dispatchID
	return job
}

func (s *OpenAIIQService) executeIQGroup(ctx context.Context, run *IQRun, job iqGroupJob) IQResult {
	result := job.result
	output, responseModel, err := s.requestIQ(ctx, job.key, job.body, job.dispatchID)
	result.Output = output
	result.ResponseModel = responseModel
	result.Status = "error"
	if err != nil {
		result.Error = "检测请求失败或回答未完整结束，待费用确认后结算"
		return result
	}
	answer, ok := ExtractIQAnswer(output)
	if !ok {
		result.Error = "回答缺少唯一的最终答案标记，无法判定"
		return result
	}
	result.ModelAnswer = answer
	result.Error = ""
	result.Status = "incorrect"
	if IQAnswerCorrect(output, run.Answer) {
		result.Status = "correct"
	}
	return result
}

func buildIQRequest(run *IQRun, maxOutput int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"model":             run.Model,
		"input":             run.Question + "\n\n请说明推理过程。最后一个非空行必须严格写成 FINAL_ANSWER: 答案，只写最终答案，不要附单位、Markdown 或其他文字。",
		"max_output_tokens": maxOutput,
		"stream":            true,
		"store":             false,
	})
}

// estimateIQCost applies the site's upstream account pricing to a conservative
// input byte allowance and full output ceiling. Final settlement uses immutable
// gateway usage snapshots because provider-added input can exceed this estimate.
func (s *OpenAIIQService) estimateIQCost(ctx context.Context, group *Group, model string, body []byte, maxOutput int) (float64, error) {
	if s.billing == nil || !s.billing.HasIdentifiedTokenPricing(model) || s.accounts == nil {
		return 0, fmt.Errorf("identified token pricing is required")
	}
	tokens := UsageTokens{InputTokens: len(body) + 1024, OutputTokens: maxOutput}
	tier := ""
	if group.ForceOpenAIFast {
		tier = "priority"
	}
	resolver := NewModelPricingResolver(s.channels, s.billing)
	cost, err := s.billing.CalculateCostUnified(CostInput{
		Ctx: ctx, Model: model, GroupID: &group.ID, Group: group, Tokens: tokens,
		RequestCount: 1, RateMultiplier: 1, ServiceTier: tier,
		ReasoningEffort: "xhigh", Resolver: resolver,
	})
	if err != nil || cost == nil || !isFiniteIQCost(cost.TotalCost) {
		return 0, fmt.Errorf("invalid group pricing")
	}
	upper := float64(0)
	accounts, err := s.accounts.ListByGroup(ctx, group.ID)
	if err != nil || len(accounts) == 0 {
		return 0, fmt.Errorf("account pricing is unavailable")
	}
	eligible := 0
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		eligible++
		mapped, _ := account.ResolveMappedModel(model)
		if !s.billing.HasIdentifiedTokenPricing(mapped) {
			return 0, fmt.Errorf("mapped model pricing is unavailable")
		}
		base, err := s.billing.CalculateCostWithServiceTier(mapped, tokens, 1, tier)
		if err != nil || base == nil {
			return 0, fmt.Errorf("mapped model cost is unavailable")
		}
		accountCost := base.TotalCost
		if override := resolveAccountStatsCost(ctx, s.channels, s.billing, account.ID, group.ID, mapped, tokens, 1, cost.TotalCost, tier, time.Now(), "xhigh"); override != nil {
			accountCost = *override
		}
		accountCost *= account.BillingRateMultiplier()
		if !isFiniteIQCost(accountCost) {
			return 0, fmt.Errorf("invalid account cost")
		}
		upper = math.Max(upper, accountCost)
	}
	if eligible == 0 {
		return 0, fmt.Errorf("schedulable account pricing is unavailable")
	}
	return math.Ceil(upper*1e8) / 1e8, nil
}

func isFiniteIQCost(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
