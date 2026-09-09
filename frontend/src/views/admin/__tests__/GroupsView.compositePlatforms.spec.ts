import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

// 上游 v0.1.178 这个用例断言 GroupsView 里存在 compositeRoutePlatformOptions
// （一个"按平台选路由目标"的下拉，含 Kimi / Zhipu GLM / DeepSeek）。
//
// aicat 不采用那种 UI：复合路由必须委托到**具体目标分组**（target_group_id 必填，
// 平台由目标分组派生），所以模板里用的是 compositeTargetGroupOptions 分组选择器，
// 上游那个 computed 在这里是无人引用的死代码，已在合并时删除。
// 因此本用例改为锁 aicat 的 UI 形状；CN 三家能否作为路由目标，取决于是否存在
// 对应平台的具体分组，由后端 isConcreteRequestPlatform 保证（见 Go 侧
// TestCompositeConcretePlatformsIncludeCNProviders）。
describe('GroupsView Composite route options', () => {
  const source = readFileSync(resolve('src/views/admin/GroupsView.vue'), 'utf8')

  it('offers concrete target groups (not platforms) as composite route targets', () => {
    expect(source).toContain('const compositeTargetGroupOptions')

    const options = source.slice(
      source.indexOf('const compositeTargetGroupOptions'),
      source.indexOf('const compositeTargetGroupName')
    )
    // 只列出「具体平台 + 启用中 + 非当前组合分组自身」的分组。
    expect(options).toContain('g.platform !== "composite"')
    expect(options).toContain('g.status === "active"')
    expect(options).toContain('g.id !== currentId')
  })

  it('binds the target-group picker in the template', () => {
    expect(source).toContain(':options="compositeTargetGroupOptions"')
  })

  it('does not reintroduce the upstream platform picker', () => {
    // 反向断言：上游那个按平台选的 computed 不得被同步回来——它与
    // 「必须委托到具体目标分组」相冲突，且会变成无人引用的死代码。
    expect(source).not.toContain('const compositeRoutePlatformOptions')
  })

  // 上游 v0.1.179 把这个用例整体换成了对共享平台目录的断言。那条断言与
  // aicat 的 UI 形状无关（目录是筛选器/表单共用的），保留下来仍有价值：
  // 它守的是「CN 三家在共享目录里算具体平台」，而具体平台正是可作为委托目标的前提。
  it('keeps Kimi, Zhipu GLM, and DeepSeek in the shared concrete-platform catalog', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(
      expect.arrayContaining(['kimi', 'zhipu', 'deepseek', 'minimax'])
    )
  })
})
