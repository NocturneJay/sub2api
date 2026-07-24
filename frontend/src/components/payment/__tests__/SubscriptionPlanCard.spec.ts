import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import { createPinia } from "pinia";
import { createI18n } from "vue-i18n";
import type { SubscriptionPlan } from "@/types/payment";
import SubscriptionPlanCard from "../SubscriptionPlanCard.vue";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  fallbackWarn: false,
  missingWarn: false,
  messages: {
    en: {
      payment: {
        days: "days",
        weeks: "weeks",
        months: "months",
        perMonth: "month",
        models: "Models",
        planCard: {
          quota: "Quota",
          rate: "Rate",
          peakRate: "Peak Rate",
          pricedByRoute: "Priced by request model group",
          routePricing: "Group rates",
          availableGroups: "Available groups",
          noCompositeRoutes: "No available model routes configured",
          unlimited: "Unlimited",
        },
        subscribeNow: "Subscribe now",
      },
    },
  },
});

const mountPlanCard = (groupPlatform: string, overrides: Partial<SubscriptionPlan> = {}) =>
  mount(SubscriptionPlanCard, {
    props: {
      plan: {
        id: 1,
        group_id: 10,
        group_platform: groupPlatform,
        name: "Pro",
        price: 10,
        amount: 1000,
        features: [],
        rate_multiplier: 1,
        validity_days: 30,
        validity_unit: "day",
        supported_model_scopes: ["claude", "gemini_text", "gemini_image"],
        is_active: true,
        ...overrides,
      },
    },
    global: { plugins: [i18n, createPinia()] },
  });

describe("SubscriptionPlanCard", () => {
  it("does not show Antigravity model scopes for OpenAI plans", () => {
    const text = mountPlanCard("openai").text();

    expect(text).not.toContain("Claude");
    expect(text).not.toContain("Gemini");
    expect(text).not.toContain("Imagen");
  });

  it("shows model scopes for Antigravity plans", () => {
    const text = mountPlanCard("antigravity").text();

    expect(text).toContain("Claude");
    expect(text).toContain("Gemini");
    expect(text).toContain("Imagen");
  });

  // #4607：管理端保存的单位是复数（months/weeks），此前用户侧只匹配单数
  // 'month'，「1 个月」的套餐卡片被显示成「1天」。测试环境的 vue-i18n 为
  // runtime-only 构建，t() 原样返回 key，故按 key 断言单位分支。
  it("renders plural admin-form validity units instead of mislabeled days (#4607)", () => {
    expect(mountPlanCard("openai", { validity_days: 1, validity_unit: "months" }).text()).toContain("/ payment.perMonth");
    expect(mountPlanCard("openai", { validity_days: 3, validity_unit: "months" }).text()).toContain("/ 3payment.months");
    expect(mountPlanCard("openai", { validity_days: 2, validity_unit: "weeks" }).text()).toContain("/ 2payment.weeks");
    expect(mountPlanCard("openai", { validity_days: 30, validity_unit: "day" }).text()).toContain("/ 30payment.days");
  });

  it("uses the configured currency symbol while preserving USD for legacy plans", () => {
    const cnyPlan = mountPlanCard("openai", { currency: "CNY", original_price: 20 }).text();

    expect(cnyPlan).toContain("¥10CNY");
    expect(cnyPlan).toContain("¥20CNY");
    expect(mountPlanCard("openai", { currency: "USD" }).text()).toContain("$10USD");
    expect(mountPlanCard("openai", { currency: "" }).text()).toContain("$10");
  });

  it("shows each effective target-group rate once for composite plans", () => {
    const wrapper = mountPlanCard("composite", {
      rate_multiplier: 9,
      peak_rate_enabled: true,
      peak_start: "09:00",
      peak_end: "18:00",
      peak_rate_multiplier: 3,
      composite_route_pricing: [
        {
          public_model: "openrouter/gpt-5",
          match_type: "exact",
          endpoint: "responses",
          target_group_id: 42,
          target_group_name: "OpenAI standard",
          target_platform: "openai",
          rate_multiplier: 1.25,
          rate_source: "target_group",
        },
        {
          public_model: "codex",
          match_type: "prefix",
          endpoint: "any",
          target_group_id: 42,
          target_group_name: "OpenAI standard",
          target_platform: "openai",
          rate_multiplier: 1.25,
          rate_source: "route",
        },
        {
          public_model: "claude",
          match_type: "prefix",
          endpoint: "messages",
          target_group_id: 43,
          target_group_name: "Claude Max",
          target_platform: "anthropic",
          rate_multiplier: 0.8,
          rate_source: "route",
        },
        {
          public_model: "deepseek",
          match_type: "prefix",
          endpoint: "any",
          target_group_id: 44,
          target_group_name: "DeepSeek/Kimi/GLM",
          target_platform: "openai",
          rate_multiplier: 0.45,
          rate_source: "route",
        },
        {
          public_model: "kimi",
          match_type: "prefix",
          endpoint: "any",
          target_group_id: 44,
          target_group_name: "DeepSeek/Kimi/GLM",
          target_platform: "openai",
          rate_multiplier: 0.45,
          rate_source: "route",
        },
        {
          public_model: "glm",
          match_type: "exact",
          endpoint: "any",
          target_group_id: 44,
          target_group_name: "DeepSeek/Kimi/GLM",
          target_platform: "openai",
          rate_multiplier: 0.45,
          rate_source: "target_group",
        },
      ],
    });
    const text = wrapper.text();

    expect(text).toContain("payment.planCard.pricedByRoute");
    expect(text).toContain("payment.planCard.availableGroups");
    expect(text).toContain("payment.planCard.rate");
    expect(text).not.toContain("payment.planCard.routePricing");
    expect(wrapper.get('[data-testid="composite-group-pricing-header"]').text()).toBe(
      "payment.planCard.availableGroupspayment.planCard.rate",
    );
    expect(text).toContain("OpenAI standard");
    expect(text.match(/OpenAI standard/g)).toHaveLength(1);
    expect(text).toContain("Claude Max");
    expect(text.match(/DeepSeek\/Kimi\/GLM/g)).toHaveLength(1);
    expect(text).toContain("×1.25");
    expect(text).toContain("×0.8");
    expect(text).toContain("×0.45");
    expect(wrapper.findAll('[data-testid="composite-group-pricing-row"]')).toHaveLength(3);
    expect(text).not.toContain("openrouter/gpt-5");
    expect(text).not.toContain("codex");
    expect(text).not.toContain("claude");
    expect(text).not.toContain("deepseek");
    expect(text).not.toContain("kimi");
    expect(text).not.toContain("glm");
    expect(text).not.toContain("payment.planCard.routeOverride");
    expect(text).not.toContain("payment.planCard.targetGroupRate");
    expect(text).not.toContain("×9");
    expect(text).not.toContain("payment.planCard.peakRate");
  });
});
