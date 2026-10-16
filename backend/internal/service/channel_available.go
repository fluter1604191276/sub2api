package service

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// AvailableGroupRef 渠道视图中关联分组的简要信息。
//
// 用户侧「可用渠道」页面据此展示：专属分组 vs 公开分组（IsExclusive）、
// 订阅 vs 标准（SubscriptionType）、默认倍率（RateMultiplier）与高峰倍率规则。
// 用户专属倍率不在这里暴露，前端自己通过 /groups/rates 拉取，和 API 密钥页面保持一致。
type AvailableGroupRef struct {
	ID                 int64
	Name               string
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
}

// AvailableChannel 可用渠道视图：用于「可用渠道」页面展示渠道基础信息 +
// 关联的分组 + 推导出的支持模型列表（无通配符）。
type AvailableChannel struct {
	ID                 int64
	Name               string
	Description        string
	Status             string
	BillingModelSource string
	RestrictModels     bool
	Groups             []AvailableGroupRef
	SupportedModels    []SupportedModel
	// GroupModels is the account-derived, group-scoped model catalogue. It is
	// non-nil when AccountModelCatalog is wired; SupportedModels remains the
	// channel-wide union for administrative/legacy consumers.
	GroupModels map[int64][]SupportedModel
}

// ListAvailable 返回所有渠道的可用视图：每个渠道附带关联分组信息与支持模型列表。
//
// 注入 AccountModelCatalog 后，支持模型由持久可用账户的有限模型证据投影，
// 再按渠道映射、限制与分组白名单过滤；未注入时保留历史渠道配置视图供旧测试使用。
// 对于渠道未配置定价的模型，进一步用 PricingService 的全局 LiteLLM 数据合成
// 一份展示用定价，让用户看到默认价格而非"未配置"。
//
// 关联分组信息通过 groupRepo.ListActive 查询后按 ID 映射；渠道 GroupIDs 中未在活跃列表中
// 的分组（已停用或删除）会被忽略。
//
// 前置条件：s.groupRepo 必须非 nil（由 wire DI 保证）。直接 nil-deref 用于 fail-fast，
// 避免静默掩盖注入缺失。
func (s *ChannelService) ListAvailable(ctx context.Context) ([]AvailableChannel, error) {
	channels, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}

	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}
	groupByID := make(map[int64]AvailableGroupRef, len(groups))
	groupEntityByID := make(map[int64]*Group, len(groups))
	for i := range groups {
		g := groups[i]
		groupEntityByID[g.ID] = &groups[i]
		groupByID[g.ID] = AvailableGroupRef{
			ID:                 g.ID,
			Name:               g.Name,
			Platform:           g.Platform,
			SubscriptionType:   g.SubscriptionType,
			RateMultiplier:     g.RateMultiplier,
			PeakRateEnabled:    g.PeakRateEnabled,
			PeakStart:          g.PeakStart,
			PeakEnd:            g.PeakEnd,
			PeakRateMultiplier: g.PeakRateMultiplier,
			IsExclusive:        g.IsExclusive,
		}
	}
	var accountsByGroup map[int64][]Account
	var compositeRoutes map[int64][]CompositeModelRoute
	if s.modelCatalog != nil {
		accountsByGroup, err = s.modelCatalog.Load(ctx, groups)
		if err != nil {
			return nil, fmt.Errorf("load account model catalog: %w", err)
		}
		compositeRoutes, err = s.modelCatalog.LoadCompositeCatalogRoutes(ctx, groups)
		if err != nil {
			return nil, fmt.Errorf("load composite catalog routes: %w", err)
		}
	}

	out := make([]AvailableChannel, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		catalogCache := accountCatalogChannelCache(ch, groups)
		groups := make([]AvailableGroupRef, 0, len(ch.GroupIDs))
		for _, gid := range ch.GroupIDs {
			if ref, ok := groupByID[gid]; ok {
				groups = append(groups, ref)
			}
		}
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

		ch.normalizeBillingModelSource()

		supported := ch.SupportedModels()
		groupModels := map[int64][]SupportedModel(nil)
		if accountsByGroup != nil {
			groupModels = make(map[int64][]SupportedModel, len(groups))
			union := make([]SupportedModel, 0)
			unionSeen := make(map[string]struct{})
			for _, group := range groups {
				if !channelHasGroup(ch, group.ID) {
					continue
				}
				projected, err := s.modelCatalog.ProjectChannelWithCacheAndRoutes(ctx, catalogCache, ch, groupEntityByID[group.ID], accountsByGroup[group.ID], compositeRoutes[group.ID])
				if err != nil {
					return nil, fmt.Errorf("project account model catalog: %w", err)
				}
				fillGlobalPricingFallback(s.pricingService, projected)
				groupModels[group.ID] = projected
				for _, model := range projected {
					key := model.Platform + "\x00" + strings.ToLower(model.Name)
					if _, exists := unionSeen[key]; exists {
						continue
					}
					unionSeen[key] = struct{}{}
					union = append(union, model)
				}
			}
			sort.SliceStable(union, func(i, j int) bool {
				if union[i].Platform != union[j].Platform {
					return union[i].Platform < union[j].Platform
				}
				return strings.ToLower(union[i].Name) < strings.ToLower(union[j].Name)
			})
			supported = union
		} else {
			fillGlobalPricingFallback(s.pricingService, supported)
		}

		out = append(out, AvailableChannel{
			ID:                 ch.ID,
			Name:               ch.Name,
			Description:        ch.Description,
			Status:             ch.Status,
			BillingModelSource: ch.BillingModelSource,
			RestrictModels:     ch.RestrictModels,
			Groups:             groups,
			SupportedModels:    supported,
			GroupModels:        groupModels,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func channelHasGroup(channel *Channel, groupID int64) bool {
	if channel == nil {
		return false
	}
	for _, id := range channel.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// channelCatalogModels keeps account-derived membership separate from the
// channel's persisted price cards. RestrictModels remains an admission rule;
// otherwise a configured account model may be shown with nil pricing until an
// administrator configures a price or the global display fallback has one.
func (c *AccountModelCatalog) ProjectChannel(ctx context.Context, channel *Channel, group *Group, accounts []Account) ([]SupportedModel, error) {
	if group == nil {
		return nil, nil
	}
	routes, err := c.LoadCompositeCatalogRoutes(ctx, []Group{*group})
	if err != nil {
		return nil, err
	}
	return c.ProjectChannelWithCacheAndRoutes(ctx, accountCatalogChannelCache(channel, []Group{*group}), channel, group, accounts, routes[group.ID])
}

func (c *AccountModelCatalog) ProjectChannelWithCacheAndRoutes(ctx context.Context, cache *channelCache, channel *Channel, group *Group, accounts []Account, routes []CompositeModelRoute) ([]SupportedModel, error) {
	return channelCatalogModels(ctx, c, cache, channel, group, accounts, routes)
}

func accountCatalogChannelCache(channel *Channel, groups []Group) *channelCache {
	if channel == nil {
		return newEmptyChannelCache()
	}
	platforms := make(map[int64]string, len(channel.GroupIDs))
	for _, group := range groups {
		if channelHasGroup(channel, group.ID) {
			platforms[group.ID] = group.Platform
		}
	}
	return populateChannelCache([]Channel{*channel}, platforms)
}

func channelCatalogModels(ctx context.Context, catalog *AccountModelCatalog, cache *channelCache, channel *Channel, group *Group, accounts []Account, routes []CompositeModelRoute) ([]SupportedModel, error) {
	if channel == nil || group == nil {
		return nil, nil
	}
	if cache == nil {
		cache = accountCatalogChannelCache(channel, []Group{*group})
	}
	seen := make(map[string]struct{})
	out := make([]SupportedModel, 0)
	add := func(platform, name string, account *Account, upstreamOverride string) {
		if name == "" || account == nil {
			return
		}
		// Composite routing replaces the request model before channel mapping
		// and restriction checks. Keep the public ID only for display/allowlists.
		routingModel := name
		if upstreamOverride != "" {
			routingModel = upstreamOverride
		}
		mapped := lookupMappingAcrossPlatforms(cache, group.ID, platform, strings.ToLower(routingModel))
		if mapped == "" {
			mapped = routingModel
		}
		if !account.IsModelSupported(mapped) || !group.ModelAllowlist.Allows(name) {
			return
		}
		restrictionModel := billingModelForRestriction(channel.BillingModelSource, routingModel, mapped)
		if restrictionModel == "" {
			restrictionModel = account.GetMappedModel(mapped)
		}
		if channel.RestrictModels && lookupPricingAcrossPlatforms(cache, group.ID, platform, strings.ToLower(restrictionModel)) == nil {
			return
		}
		// A route/account target is not an authoritative price for a public
		// alias. Keep unknown aliases unpriced rather than guessing its target.
		pricing := lookupPricingAcrossPlatforms(cache, group.ID, platform, strings.ToLower(name))
		if pricing != nil {
			copy := pricing.Clone()
			pricing = &copy
		}
		key := platform + "\x00" + strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, SupportedModel{Name: name, Platform: platform, Pricing: pricing})
	}

	candidates := make(map[string]struct{})
	for i := range accounts {
		account := &accounts[i]
		for _, name := range accountCatalogPublicModelIDs(account) {
			candidates[name] = struct{}{}
		}
		for _, mapping := range catalogChannelMappings(channel, catalogDisplayPlatform(group, account)) {
			for name := range mapping {
				if strings.TrimSpace(name) != "" && !strings.Contains(name, "*") {
					candidates[name] = struct{}{}
				}
			}
		}
	}
	for _, route := range routes {
		if normalizeCompositeRouteMatchType(route.MatchType) == CompositeRouteMatchExact {
			if name := strings.TrimSpace(route.PublicModel); name != "" && !strings.Contains(name, "*") {
				candidates[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if group.Platform != PlatformComposite {
			for i := range accounts {
				add(catalogDisplayPlatform(group, &accounts[i]), name, &accounts[i], "")
			}
			continue
		}
		// Membership is endpoint-independent: preserve each configured destination
		// that can actually accept this finite name on at least one known endpoint.
		for _, destination := range catalogCompositeDestinations(routes, name, accounts) {
			for i := range accounts {
				if accounts[i].Platform == destination.TargetPlatform {
					add(destination.TargetPlatform, name, &accounts[i], destination.UpstreamModel)
				}
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func catalogCompositeDestinations(routes []CompositeModelRoute, model string, accounts []Account) []CompositeRouteDecision {
	endpoints := []string{CompositeRouteEndpointMessages, CompositeRouteEndpointCountTokens,
		CompositeRouteEndpointResponses, CompositeRouteEndpointChatCompletions,
		CompositeRouteEndpointEmbeddings, CompositeRouteEndpointImages, CompositeRouteEndpointGemini}
	out := make([]CompositeRouteDecision, 0, 1)
	seen := make(map[string]bool)
	for _, endpoint := range endpoints {
		platform, upstream := "", model
		if route, matched := matchCompositeRoute(routes, model, endpoint); matched {
			platform = route.TargetPlatform
			if target := strings.TrimSpace(route.UpstreamModel); target != "" {
				upstream = target
			}
		} else {
			ambiguous := false
			for _, account := range accounts {
				if !explicitModelMappingClaims(account, model) {
					continue
				}
				if platform != "" && platform != account.Platform {
					ambiguous = true
					break
				}
				platform = account.Platform
			}
			if ambiguous {
				continue
			}
			if platform == "" {
				platform, _ = DetectModelPlatform(model)
			}
		}
		if !isConcreteRequestPlatform(platform) {
			continue
		}
		key := platform + "\x00" + upstream
		if !seen[key] {
			seen[key] = true
			out = append(out, CompositeRouteDecision{Matched: true, PublicModel: model, TargetPlatform: platform, UpstreamModel: upstream})
		}
	}
	return out
}

func catalogChannelMappings(channel *Channel, platform string) []map[string]string {
	if channel == nil {
		return nil
	}
	mappings := make([]map[string]string, 0, len(matchingPlatforms(platform)))
	for _, candidatePlatform := range matchingPlatforms(platform) {
		if mapping := channel.ModelMapping[candidatePlatform]; len(mapping) > 0 {
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}

// fillGlobalPricingFallback 对未命中渠道定价的支持模型，从全局 LiteLLM 数据合成一份
// 展示用定价。仅用于「可用渠道」展示，不影响真实计费链路。
//
// 触发条件：
//  1. Pricing == nil（渠道完全没声明该模型的定价条目）
//  2. Pricing 非 nil 但所有价格字段为空（admin UI 建了条目但没填价格）
//
// 当 pricingService 为 nil（测试场景），跳过价格回落。
// 可用渠道与模型广场共用。
func fillGlobalPricingFallback(pricingService *PricingService, models []SupportedModel) {
	for i := range models {
		if pricingService != nil && pricingNeedsFallback(models[i].Pricing) {
			if lp := pricingService.GetModelPricing(models[i].Name); lp != nil {
				models[i].Pricing = synthesizePricingFromLiteLLM(lp, models[i].Pricing)
			}
		}
	}
}

// pricingNeedsFallback 判定一个 ChannelModelPricing 是否需要走全局回落。
// 价格全部缺失（无 flat 字段且无任何带价 interval）即视为未配置。
func pricingNeedsFallback(p *ChannelModelPricing) bool {
	if p == nil {
		return true
	}
	if p.InputPrice != nil || p.OutputPrice != nil ||
		p.CacheWritePrice != nil || p.CacheWrite1hPrice != nil || p.CacheReadPrice != nil ||
		p.ImageInputPrice != nil || p.ImageOutputPrice != nil || p.PerRequestPrice != nil {
		return false
	}
	for _, iv := range p.Intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil ||
			iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil ||
			iv.PerRequestPrice != nil {
			return false
		}
	}
	return true
}

// synthesizePricingFromLiteLLM 把 LiteLLM 的定价数据转成 ChannelModelPricing 形态，
// 仅用于展示。
//
// 计费模式优先级：
//  1. 渠道已选 BillingMode（admin 在 UI 里选了 image / per_request 但没填价的场景，
//     按选定模式合成对应字段）
//  2. LiteLLM mode="image_generation" → image
//  3. 默认 token
//
// LiteLLM 中字段 0 视为未配置，不带入展示。
func synthesizePricingFromLiteLLM(lp *LiteLLMModelPricing, existing *ChannelModelPricing) *ChannelModelPricing {
	if lp == nil {
		return existing
	}

	mode := BillingModeToken
	switch {
	case existing != nil && existing.BillingMode != "":
		mode = existing.BillingMode
	case lp.Mode == "image_generation":
		mode = BillingModeImage
	}

	if mode == BillingModeImage || mode == BillingModePerRequest {
		return &ChannelModelPricing{
			BillingMode:                mode,
			PerRequestPrice:            nonZeroPtr(lp.OutputCostPerImage),
			ImageOutputPrice:           nonZeroPtr(lp.OutputCostPerImageToken),
			InputPrice:                 nonZeroPtr(lp.InputCostPerToken),
			OutputPrice:                nonZeroPtr(lp.OutputCostPerToken),
			ReasoningEffortMultipliers: reasoningEffortMultipliersFromPricing(existing),
		}
	}
	return &ChannelModelPricing{
		BillingMode:                mode,
		InputPrice:                 nonZeroPtr(lp.InputCostPerToken),
		OutputPrice:                nonZeroPtr(lp.OutputCostPerToken),
		CacheWritePrice:            nonZeroPtr(lp.CacheCreationInputTokenCost),
		CacheWrite1hPrice:          nonZeroPtr(lp.CacheCreationInputTokenCostAbove1hr),
		CacheReadPrice:             nonZeroPtr(lp.CacheReadInputTokenCost),
		ImageOutputPrice:           nonZeroPtr(lp.OutputCostPerImageToken),
		ReasoningEffortMultipliers: reasoningEffortMultipliersFromPricing(existing),
	}
}

func reasoningEffortMultipliersFromPricing(pricing *ChannelModelPricing) map[string]float64 {
	if pricing == nil {
		return nil
	}
	return EffectiveReasoningEffortMultipliers(pricing)
}

func nonZeroPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// EffectiveReasoningEffortMultipliers preserves legacy explicit max values when
// persisted through the new map representation. A supplied map is canonical,
// including an empty map; cleared overrides must not revive the legacy scalar.
func EffectiveReasoningEffortMultipliers(pricing *ChannelModelPricing) map[string]float64 {
	if pricing == nil {
		return nil
	}
	if pricing.ReasoningEffortMultipliers != nil {
		return maps.Clone(pricing.ReasoningEffortMultipliers)
	}
	var result map[string]float64
	if pricing.MaxReasoningEffortMultiplier != nil {
		if _, configured := result["max"]; !configured {
			if result == nil {
				result = map[string]float64{}
			}
			result["max"] = *pricing.MaxReasoningEffortMultiplier
		}
	}
	return result
}
