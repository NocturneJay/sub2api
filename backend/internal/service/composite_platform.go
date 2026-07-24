package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// WithResolvedTargetPlatform stores the concrete provider chosen for a request
// made through a composite group.
func WithResolvedTargetPlatform(ctx context.Context, platform string) context.Context {
	platform = strings.TrimSpace(platform)
	if ctx == nil || platform == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.ResolvedTargetPlatform, platform)
}

// ResolvedTargetPlatformFromContext returns the concrete provider chosen for
// the current request, if one was resolved.
func ResolvedTargetPlatformFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	platform, ok := ctx.Value(ctxkey.ResolvedTargetPlatform).(string)
	platform = strings.TrimSpace(platform)
	if !ok || platform == "" {
		return "", false
	}
	return platform, true
}

func WithCompositeRouteDecision(ctx context.Context, decision CompositeRouteDecision) context.Context {
	if ctx == nil || !decision.Matched {
		return ctx
	}
	ctx = WithResolvedTargetPlatform(ctx, decision.TargetPlatform)
	if model := strings.TrimSpace(decision.UpstreamModel); model != "" {
		ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, model)
	}
	if model := strings.TrimSpace(decision.PublicModel); model != "" {
		ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, model)
	}
	if source := strings.TrimSpace(decision.Source); source != "" {
		ctx = context.WithValue(ctx, ctxkey.CompositeRouteSource, source)
	}
	// 委托到子分组：记录定价/调度分组与倍率覆盖，供计费与重试链路复用。
	if decision.TargetGroupID != nil && *decision.TargetGroupID > 0 {
		ctx = context.WithValue(ctx, ctxkey.ResolvedPricingGroupID, *decision.TargetGroupID)
		if decision.RateMultiplier != nil && *decision.RateMultiplier > 0 {
			ctx = context.WithValue(ctx, ctxkey.ResolvedRateMultiplier, *decision.RateMultiplier)
		}
	}
	return ctx
}

// WithResolvedPricingGroupID stores the sub-group whose pricing should apply to a
// request delegated through a composite group. Only billing (rate multiplier and
// channel pricing lookup) consumes it; quota/limits/balance stay on apiKey.Group.
func WithResolvedPricingGroupID(ctx context.Context, groupID int64) context.Context {
	if ctx == nil || groupID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.ResolvedPricingGroupID, groupID)
}

// ResolvedPricingGroupIDFromContext returns the sub-group chosen to price a
// composite-delegated request, if one was resolved.
func ResolvedPricingGroupIDFromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	id, ok := ctx.Value(ctxkey.ResolvedPricingGroupID).(int64)
	if !ok || id <= 0 {
		return 0, false
	}
	return id, true
}

// ResolvedRateMultiplierFromContext returns the per-route rate multiplier override
// for a composite-delegated request, if one was configured.
func ResolvedRateMultiplierFromContext(ctx context.Context) (float64, bool) {
	if ctx == nil {
		return 0, false
	}
	m, ok := ctx.Value(ctxkey.ResolvedRateMultiplier).(float64)
	if !ok || m <= 0 {
		return 0, false
	}
	return m, true
}

// effectiveCompositeTargetGroupID returns the delegated sub-group ID for
// request-scoped group operations. Non-delegated requests keep the caller's
// original group ID.
func effectiveCompositeTargetGroupID(ctx context.Context, groupID *int64) *int64 {
	if id, ok := ResolvedPricingGroupIDFromContext(ctx); ok {
		resolvedID := id
		return &resolvedID
	}
	return groupID
}

// resolveCompositeDelegatedGroup validates the persisted delegation again at
// request time. This keeps scheduling and pricing on one concrete, active group
// even if an administrator changes or disables the target after the route was
// saved.
func resolveCompositeDelegatedGroup(ctx context.Context, repo GroupRepository) (*Group, *int64, error) {
	targetID, ok := ResolvedPricingGroupIDFromContext(ctx)
	if !ok {
		return nil, nil, nil
	}
	if repo == nil {
		return nil, nil, fmt.Errorf("%w (composite target group repository unavailable)", ErrNoAvailableAccounts)
	}
	target, err := repo.GetByIDLite(ctx, targetID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w (composite target group %d unavailable: %v)", ErrNoAvailableAccounts, targetID, err)
	}
	if target == nil || !target.IsActive() || !isConcreteRequestPlatform(target.Platform) {
		return nil, nil, fmt.Errorf("%w (composite target group %d is not active and concrete)", ErrNoAvailableAccounts, targetID)
	}
	if resolvedPlatform, platformOK := ResolvedTargetPlatformFromContext(ctx); platformOK && resolvedPlatform != target.Platform {
		return nil, nil, fmt.Errorf("%w (composite target group %d platform changed from %s to %s)", ErrNoAvailableAccounts, targetID, resolvedPlatform, target.Platform)
	}
	id := targetID
	return target, &id, nil
}

// compositeDelegatedPricingAPIKey returns a shallow API key snapshot whose
// Group points at the delegated pricing group. Callers must continue using the
// original API key for subscription, balance, limits, quota and usage-log
// ownership.
func compositeDelegatedPricingAPIKey(ctx context.Context, apiKey *APIKey, repo GroupRepository) (*APIKey, bool, error) {
	target, targetID, err := resolveCompositeDelegatedGroup(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	if target == nil {
		return apiKey, false, nil
	}
	if apiKey == nil {
		return nil, false, fmt.Errorf("composite delegated pricing requires an API key")
	}
	clone := *apiKey
	clone.GroupID = targetID
	clone.Group = target
	return &clone, true, nil
}

func ResolvedUpstreamModelFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	model, ok := ctx.Value(ctxkey.ResolvedUpstreamModel).(string)
	model = strings.TrimSpace(model)
	if !ok || model == "" {
		return "", false
	}
	return model, true
}

func RequestedPublicModelFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	model, ok := ctx.Value(ctxkey.RequestedPublicModel).(string)
	model = strings.TrimSpace(model)
	if !ok || model == "" {
		return "", false
	}
	return model, true
}

func CompositeRouteSourceFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	source, ok := ctx.Value(ctxkey.CompositeRouteSource).(string)
	source = strings.TrimSpace(source)
	if !ok || source == "" {
		return "", false
	}
	return source, true
}

// DetectModelPlatform maps common public model IDs to the concrete provider
// platform used by sub2api. It intentionally returns false for ambiguous model
// names so composite groups fail closed instead of guessing.
func DetectModelPlatform(model string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return "", false
	}

	normalized = strings.TrimPrefix(normalized, "models/")
	if slash := strings.IndexByte(normalized, '/'); slash > 0 {
		provider := strings.TrimSpace(normalized[:slash])
		rest := strings.TrimSpace(normalized[slash+1:])
		switch provider {
		case "anthropic", "claude":
			return PlatformAnthropic, true
		case "openai", "chatgpt":
			return PlatformOpenAI, true
		case "google", "google-ai-studio", "gemini":
			return PlatformGemini, true
		case "xai", "x-ai", "grok":
			return PlatformGrok, true
		}
		if rest != "" {
			normalized = strings.TrimPrefix(rest, "models/")
		}
	}

	switch {
	case strings.HasPrefix(normalized, "anthropic.claude-"),
		strings.HasPrefix(normalized, "claude-"):
		return PlatformAnthropic, true
	case strings.HasPrefix(normalized, "gpt-"),
		strings.HasPrefix(normalized, "chatgpt-"),
		strings.HasPrefix(normalized, "codex-"),
		strings.HasPrefix(normalized, "text-embedding-"),
		strings.HasPrefix(normalized, "text-moderation-"),
		strings.HasPrefix(normalized, "omni-moderation-"),
		strings.HasPrefix(normalized, "dall-e-"),
		strings.HasPrefix(normalized, "gpt-image-"),
		strings.HasPrefix(normalized, "tts-"),
		strings.HasPrefix(normalized, "whisper-"),
		hasOpenAISeriesPrefix(normalized):
		return PlatformOpenAI, true
	case strings.HasPrefix(normalized, "gemini-"),
		strings.HasPrefix(normalized, "learnlm-"):
		return PlatformGemini, true
	case normalized == "grok" || strings.HasPrefix(normalized, "grok-"):
		return PlatformGrok, true
	default:
		return "", false
	}
}

func hasOpenAISeriesPrefix(model string) bool {
	for _, prefix := range []string{"o1", "o3", "o4", "o5"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			return true
		}
	}
	return false
}

func (s *GatewayService) resolveCompositeRouteDecision(ctx context.Context, group *Group, requestedModel, endpoint string) (CompositeRouteDecision, bool, error) {
	if group == nil || group.Platform != PlatformComposite {
		return CompositeRouteDecision{}, false, nil
	}
	if platform, ok := ResolvedTargetPlatformFromContext(ctx); ok {
		upstreamModel := requestedModel
		if resolvedModel, modelOK := ResolvedUpstreamModelFromContext(ctx); modelOK {
			upstreamModel = resolvedModel
		}
		source := CompositeRouteSourceDetector
		if resolvedSource, sourceOK := CompositeRouteSourceFromContext(ctx); sourceOK {
			source = resolvedSource
		}
		decision := CompositeRouteDecision{
			Matched:        true,
			Source:         source,
			GroupID:        group.ID,
			PublicModel:    requestedModel,
			TargetPlatform: platform,
			UpstreamModel:  upstreamModel,
			Endpoint:       normalizeCompositeRouteEndpoint(endpoint),
		}
		// 恢复"委托到子分组"信息（重试链路复用首次解析结果），保证调度仍落在子分组账号池。
		if pricingGroupID, pgOK := ResolvedPricingGroupIDFromContext(ctx); pgOK {
			pg := pricingGroupID
			decision.TargetGroupID = &pg
			if m, mOK := ResolvedRateMultiplierFromContext(ctx); mOK {
				mm := m
				decision.RateMultiplier = &mm
			}
		}
		return decision, true, nil
	}
	decision, err := s.compositeResolver.Resolve(ctx, group.ID, requestedModel, endpoint)
	if err != nil {
		return decision, false, err
	}
	if !decision.Matched {
		return decision, false, nil
	}
	// 委托到子分组：用子分组平台填充 TargetPlatform，并校验子分组合法（存在、具体平台、非自身/非 composite）。
	if decision.TargetGroupID != nil {
		if err := s.applyCompositeTargetGroup(ctx, group, &decision); err != nil {
			return decision, false, err
		}
	}
	return decision, decision.Matched, nil
}

// applyCompositeTargetGroup 解析"委托到子分组"路由的目标子分组，用其平台填充
// decision.TargetPlatform，并拒绝非法目标（自身、composite、非具体平台）。
func (s *GatewayService) applyCompositeTargetGroup(ctx context.Context, composite *Group, decision *CompositeRouteDecision) error {
	if decision == nil || decision.TargetGroupID == nil {
		return nil
	}
	targetID := *decision.TargetGroupID
	if targetID <= 0 || (composite != nil && targetID == composite.ID) {
		return fmt.Errorf("%w supporting model: %s (composite target group %d invalid)", ErrNoAvailableAccounts, decision.PublicModel, targetID)
	}
	target, err := s.resolveGroupByID(ctx, targetID)
	if err != nil {
		return err
	}
	if target == nil || !target.IsActive() || !isConcreteRequestPlatform(target.Platform) {
		return fmt.Errorf("%w supporting model: %s (composite target group %d is not active and concrete)", ErrNoAvailableAccounts, decision.PublicModel, targetID)
	}
	decision.TargetPlatform = target.Platform
	return nil
}

func isConcreteRequestPlatform(platform string) bool {
	switch platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok:
		return true
	default:
		return false
	}
}
