package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUserSubscriptionDTOIncludesCompositeRoutePricing(t *testing.T) {
	subscription := &service.UserSubscription{
		ID:      9,
		GroupID: 35,
	}
	pricing := map[int64][]service.CompositeRoutePricingInfo{
		35: {
			{
				PublicModel:               "gpt",
				MatchType:                 service.CompositeRouteMatchPrefix,
				TargetGroupID:             7,
				TargetGroupName:           "GPT PRO",
				TargetPlatform:            service.PlatformOpenAI,
				TargetGroupRateMultiplier: 0.25,
				RateMultiplier:            0.15,
				RateSource:                "route",
			},
		},
	}

	got := userSubscriptionDTO(subscription, pricing)
	if got == nil {
		t.Fatal("userSubscriptionDTO() returned nil")
	}
	if len(got.CompositeRoutePricing) != 1 {
		t.Fatalf("route pricing count = %d, want 1", len(got.CompositeRoutePricing))
	}
	route := got.CompositeRoutePricing[0]
	if route.TargetGroupID != 7 ||
		route.TargetGroupRateMultiplier != 0.25 ||
		route.RateMultiplier != 0.15 ||
		route.RateSource != "route" {
		t.Fatalf("unexpected route pricing: %+v", route)
	}
}
