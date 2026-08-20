import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function readSource(path: string): string {
  return readFileSync(resolve(path), 'utf8')
}

describe('admin platform filters', () => {
  it('uses the group platform catalog on the subscriptions page', () => {
    const source = readSource('src/views/admin/SubscriptionsView.vue')
    expect(source).toContain("import { GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'")
    expect(source).toMatch(/const platformFilterOptions[\s\S]*?\.\.\.GROUP_PLATFORM_OPTIONS/)
  })

  // 上游原版还断言 GroupsView 用了 `...CONCRETE_PLATFORM_OPTIONS`。上游那处唯一的
  // 引用点是复合路由的「按平台选目标」下拉（compositeRoutePlatformOptions），
  // 而 aicat 的复合路由必须委托到**具体目标分组**（target_group_id 必填，平台由目标
  // 分组派生），没有平台下拉，那个 computed 在这里是死代码——见
  // GroupsView.compositePlatforms.spec.ts。
  // 因此这里只保留分组平台目录的断言，并补一条反向断言守住这条守则的本意：
  // 页面不得手搓平台字面量绕开共享目录（否则新接入的供应商会静默消失）。
  it('uses the shared group-platform catalog on the groups page', () => {
    const source = readSource('src/views/admin/GroupsView.vue')
    expect(source).toContain("import { GROUP_PLATFORM_OPTIONS } from \"@/constants/platforms\"")
    expect(source).toContain('...GROUP_PLATFORM_OPTIONS')
    // 反向：不得出现手写的平台选项字面量。
    expect(source).not.toMatch(/\{\s*value:\s*["']openai["']\s*,\s*label:/)
    expect(source).not.toMatch(/\{\s*value:\s*["']anthropic["']\s*,\s*label:/)
  })

  it('uses the concrete platform catalog wherever concrete platforms are selected', () => {
    for (const path of [
      'src/components/admin/account/AccountTableFilters.vue',
      'src/components/admin/ErrorPassthroughRulesModal.vue',
      'src/views/admin/ops/components/OpsDashboardHeader.vue'
    ]) {
      const source = readSource(path)
      expect(source).toContain("import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'")
      expect(source).toMatch(/platformOptions\s*=.*CONCRETE_PLATFORM_OPTIONS|pOpts.*\.\.\.CONCRETE_PLATFORM_OPTIONS/s)
    }
  })
})
