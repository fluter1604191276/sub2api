package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// AccountModelCatalog is the read-only, finite model source used by public
// catalogue views. It deliberately reads persistently eligible accounts: a
// temporary upstream rate limit must not make a configured model disappear
// from a price card, while a disabled account must not advertise a model.
//
// It never calls an upstream model-list endpoint. The account's configured
// mapping is the public routing contract; available_models is the bounded
// snapshot used to expand wildcard, empty-mapping, and passthrough accounts.
type AccountModelCatalog struct {
	accountRepo            AccountRepository
	compositeRouteResolver *CompositeRouteResolver
}

func NewAccountModelCatalog(accountRepo AccountRepository) *AccountModelCatalog {
	return &AccountModelCatalog{accountRepo: accountRepo}
}

func (c *AccountModelCatalog) SetCompositeRouteResolver(resolver *CompositeRouteResolver) {
	if c != nil {
		c.compositeRouteResolver = resolver
	}
}

// LoadCompositeCatalogRoutes reads each active composite group's enabled routes
// once per request. Projection uses the runtime matcher without its live,
// transient account-ownership callback.
func (c *AccountModelCatalog) LoadCompositeCatalogRoutes(ctx context.Context, groups []Group) (map[int64][]CompositeModelRoute, error) {
	out := make(map[int64][]CompositeModelRoute)
	if c == nil || c.compositeRouteResolver == nil || c.compositeRouteResolver.repo == nil {
		return out, nil
	}
	for _, group := range groups {
		if group.Platform != PlatformComposite {
			continue
		}
		if _, loaded := out[group.ID]; loaded {
			continue
		}
		routes, err := c.compositeRouteResolver.repo.ListByGroup(ctx, group.ID, false)
		if err != nil {
			return nil, err
		}
		out[group.ID] = []CompositeModelRoute{}
		for _, route := range routes {
			if route.Enabled {
				out[group.ID] = append(out[group.ID], route)
			}
		}
	}
	return out, nil
}

// Load fetches the persisted, eligible account pool once and buckets it by the
// supplied active groups. Accounts are returned only to groups they are bound
// to. A concrete group admits its own platform, plus an Antigravity account
// when the established mixed-scheduling flag permits it; composite groups
// retain each concrete provider platform.
func (c *AccountModelCatalog) Load(ctx context.Context, groups []Group) (map[int64][]Account, error) {
	out := make(map[int64][]Account, len(groups))
	if c == nil || c.accountRepo == nil || len(groups) == 0 {
		return out, nil
	}

	groupByID := make(map[int64]*Group, len(groups))
	for i := range groups {
		groupByID[groups[i].ID] = &groups[i]
		out[groups[i].ID] = []Account{}
	}

	accounts, err := c.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		nil,
		domain.ConcretePlatformIDs(),
		true,
	)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		account := accounts[i]
		// The repository applies this predicate in production. Keep it here as
		// well so a future repository implementation (and test doubles) cannot
		// make disabled accounts advertise models. Do not call IsSchedulable:
		// temporary rate-limit and overload windows are intentionally ignored.
		if !account.IsActive() || !account.Schedulable {
			continue
		}
		for _, groupID := range account.GroupIDs {
			group, ok := groupByID[groupID]
			if !ok || !accountCanSupplyCatalogGroup(&account, group) {
				continue
			}
			out[groupID] = append(out[groupID], account)
		}
	}
	return out, nil
}

func accountCanSupplyCatalogGroup(account *Account, group *Group) bool {
	if account == nil || group == nil || !isConcreteRequestPlatform(account.Platform) {
		return false
	}
	if group.Platform == PlatformComposite {
		return true
	}
	if account.Platform == group.Platform {
		return true
	}
	return account.Platform == PlatformAntigravity &&
		(account.IsMixedSchedulingEnabled()) &&
		(group.Platform == PlatformAnthropic || group.Platform == PlatformGemini)
}

// Project turns finite account evidence into public model names. Exact mapping
// keys are public aliases and remain visible even when an old snapshot no
// longer contains their target. Wildcard/empty/passthrough mappings contribute
// only names recorded in available_models; this avoids inventing a static
// provider catalogue. Channel aliases and group allowlists are intentionally
// applied by the final channel projection, where the public request ID is known.
func (c *AccountModelCatalog) Project(groups []Group, accountsByGroup map[int64][]Account) map[int64][]SupportedModel {
	out := make(map[int64][]SupportedModel, len(groups))
	for i := range groups {
		group := &groups[i]
		seen := make(map[string]struct{})
		models := make([]SupportedModel, 0)
		for j := range accountsByGroup[group.ID] {
			account := &accountsByGroup[group.ID][j]
			platform := catalogDisplayPlatform(group, account)
			for _, name := range accountCatalogPublicModelIDs(account) {
				key := platform + "\x00" + strings.ToLower(name)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				models = append(models, SupportedModel{Name: name, Platform: platform})
			}
		}

		// Group allowlists apply to the public request name. Channel aliases are
		// added later, so filtering here would incorrectly drop an allowed alias.
		sort.SliceStable(models, func(a, b int) bool {
			if models[a].Platform != models[b].Platform {
				return models[a].Platform < models[b].Platform
			}
			return strings.ToLower(models[a].Name) < strings.ToLower(models[b].Name)
		})
		out[group.ID] = models
	}
	return out
}

func catalogDisplayPlatform(group *Group, account *Account) string {
	if group != nil && group.Platform != PlatformComposite {
		return group.Platform
	}
	if account == nil {
		return ""
	}
	return account.Platform
}

func supportedModelNames(models []SupportedModel) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	return names
}

func accountCatalogPublicModelIDs(account *Account) []string {
	if account == nil {
		return nil
	}
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	add := func(raw string) {
		id := normalizeDiscoveredModelID(raw)
		if id == "" || strings.Contains(id, "*") {
			return
		}
		key := strings.ToLower(id)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		ids = append(ids, id)
	}

	if !account.IsOpenAIPassthroughEnabled() {
		// Exact configured keys are the public request IDs. Never replace them
		// with their mapping target: that target is provider-facing and may be
		// private. Passthrough ignores this mapping entirely, so stale keys must
		// not become catalogue entries in that mode.
		for source, target := range account.GetModelMapping() {
			if strings.TrimSpace(target) != "" {
				add(source)
			}
		}
	}
	// A snapshot makes otherwise infinite account configuration finite. Mapping
	// admission preserves aliases and wildcard restrictions without exposing raw
	// targets that the account does not itself accept as a request model.
	for _, id := range readAccountModelIDs(account.Extra[AccountAvailableModelsExtraKey]) {
		if account.IsModelSupported(id) {
			add(id)
		}
	}
	sort.SliceStable(ids, func(i, j int) bool { return strings.ToLower(ids[i]) < strings.ToLower(ids[j]) })
	return ids
}
