package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	CompositeRouteMatchExact  = "exact"
	CompositeRouteMatchPrefix = "prefix"

	CompositeRouteEndpointAny             = "any"
	CompositeRouteEndpointMessages        = "messages"
	CompositeRouteEndpointCountTokens     = "count_tokens"
	CompositeRouteEndpointResponses       = "responses"
	CompositeRouteEndpointChatCompletions = "chat_completions"
	CompositeRouteEndpointEmbeddings      = "embeddings"
	CompositeRouteEndpointImages          = "images"
	CompositeRouteEndpointGemini          = "gemini"

	CompositeRouteSourceExplicit = "route"
	CompositeRouteSourceDetector = "detector"
)

var (
	ErrCompositeRouteNotFound = infraerrors.NotFound("COMPOSITE_ROUTE_NOT_FOUND", "composite route not found")
	ErrCompositeRouteExists   = infraerrors.Conflict("COMPOSITE_ROUTE_EXISTS", "composite route already exists")
)

// CompositeModelRoute maps one public model identifier in a composite group to
// the concrete provider/model that should handle the request.
type CompositeModelRoute struct {
	ID             int64     `json:"id"`
	GroupID        int64     `json:"group_id"`
	PublicModel    string    `json:"public_model"`
	MatchType      string    `json:"match_type"`
	TargetPlatform string    `json:"target_platform"`
	// TargetGroupID 非空表示该路由"委托到子分组"：请求由该子分组的账号池调度，
	// 并按该子分组定价计费；配额/限额/扣费仍记在 composite（通用）分组头上。
	// 与 TargetPlatform 二选一。
	TargetGroupID *int64 `json:"target_group_id,omitempty"`
	// RateMultiplier 为委托路由的每路由倍率覆盖；nil 表示沿用子分组自身倍率。
	RateMultiplier *float64 `json:"rate_multiplier,omitempty"`
	UpstreamModel  string    `json:"upstream_model"`
	Endpoint       string    `json:"endpoint"`
	Priority       int       `json:"priority"`
	Enabled        bool      `json:"enabled"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CompositeRoutePreviewRequest struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
}

type CompositeRouteDecision struct {
	Matched        bool                 `json:"matched"`
	Source         string               `json:"source"`
	GroupID        int64                `json:"group_id"`
	PublicModel    string               `json:"public_model"`
	TargetPlatform string               `json:"target_platform"`
	// TargetGroupID 非空表示命中"委托到子分组"路由；调度改用该子分组的账号池，
	// 计费改用该子分组定价（RateMultiplier 为生效倍率覆盖，nil 表示沿用子分组倍率）。
	TargetGroupID  *int64               `json:"target_group_id,omitempty"`
	RateMultiplier *float64             `json:"rate_multiplier,omitempty"`
	UpstreamModel  string               `json:"upstream_model"`
	Endpoint       string               `json:"endpoint"`
	Route          *CompositeModelRoute `json:"route,omitempty"`
	Reason         string               `json:"reason,omitempty"`
}

type CompositeRouteInput struct {
	PublicModel    string
	MatchType      string
	TargetPlatform string
	TargetGroupID  *int64
	RateMultiplier *float64
	UpstreamModel  string
	Endpoint       string
	Priority       int
	Enabled        bool
	Notes          string
}

type CompositeModelRouteRepository interface {
	ListByGroup(ctx context.Context, groupID int64, includeDisabled bool) ([]CompositeModelRoute, error)
	Create(ctx context.Context, route *CompositeModelRoute) error
	Update(ctx context.Context, route *CompositeModelRoute) error
	Delete(ctx context.Context, id int64) error
	DeleteByGroup(ctx context.Context, groupID int64) error
}

func normalizeCompositeRouteEndpoint(endpoint string) string {
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	if endpoint == "" {
		return CompositeRouteEndpointAny
	}
	switch endpoint {
	case CompositeRouteEndpointMessages,
		CompositeRouteEndpointCountTokens,
		CompositeRouteEndpointResponses,
		CompositeRouteEndpointChatCompletions,
		CompositeRouteEndpointEmbeddings,
		CompositeRouteEndpointImages,
		CompositeRouteEndpointGemini:
		return endpoint
	default:
		return CompositeRouteEndpointAny
	}
}

func normalizeCompositeRouteMatchType(matchType string) string {
	matchType = strings.ToLower(strings.TrimSpace(matchType))
	switch matchType {
	case CompositeRouteMatchPrefix:
		return CompositeRouteMatchPrefix
	default:
		return CompositeRouteMatchExact
	}
}

func normalizeCompositeRouteInput(input CompositeRouteInput) CompositeRouteInput {
	input.PublicModel = strings.TrimSpace(input.PublicModel)
	input.MatchType = normalizeCompositeRouteMatchType(input.MatchType)
	input.TargetPlatform = strings.TrimSpace(input.TargetPlatform)
	input.UpstreamModel = strings.TrimSpace(input.UpstreamModel)
	input.Endpoint = normalizeCompositeRouteEndpoint(input.Endpoint)
	if input.UpstreamModel == "" {
		input.UpstreamModel = input.PublicModel
	}
	// 委托到子分组：非正数视为未设置；倍率覆盖非正数（<=0）视为未设置，沿用子分组倍率。
	if input.TargetGroupID != nil && *input.TargetGroupID <= 0 {
		input.TargetGroupID = nil
	}
	if input.TargetGroupID == nil {
		input.RateMultiplier = nil
	} else if input.RateMultiplier != nil && *input.RateMultiplier <= 0 {
		input.RateMultiplier = nil
	}
	input.Notes = strings.TrimSpace(input.Notes)
	return input
}
