import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

// 用 cwd 相对路径而不是 import.meta.url：该 spec 跑在 jsdom 环境下，
// import.meta.url 不保证是 file: scheme。vitest 的 cwd 即 frontend 根目录。
const themePath = resolve(process.cwd(), 'src/styles/purchase-plan-theme.css')
const mainPath = resolve(process.cwd(), 'src/main.ts')

/**
 * 订阅/购买页皮肤自 2026-08-04 起直接编进构建，不再依赖部署时的 CSS 覆盖脚本。
 *
 * 此前的做法是：源码里放旧版，部署后再把新版追加到 data/public/assets 下的同名
 * 文件末尾，靠源序后写的赢。代价是每次发布都必须记得跑覆盖脚本，漏一次就静默
 * 退回旧皮肤——不报错、不告警，7-31 就这么失效过一次。
 *
 * 这几条用例守住新机制的前提：主题文件被 main.ts 引入、且关键选择器齐全。
 * 任何一条挂了都说明皮肤可能已经不在构建产物里。
 */
describe('purchase plan theme', () => {
  it('is imported by main.ts so it lands in the built bundle', () => {
    const main = readFileSync(mainPath, 'utf8')
    expect(main).toContain("import './styles/purchase-plan-theme.css'")
  })

  it('selects cards by attribute, never by position', () => {
    const css = readFileSync(themePath, 'utf8')
    expect(css).toContain('data-plan-price')
    expect(css).toContain('data-plan-platform')

    // v2 皮肤按卡片自带属性定位，与卡片顺序无关；旧版按 :nth-child 选卡，
    // 加套餐或调顺序就会错位。:nth-child 本身没被禁用——卡片内部的装饰元素
    // （.aicat-plan-glints span）仍然用它，那是同一张卡内的子元素、与卡片顺序无关。
    // 这里守的是：凡用到 :nth-child 的规则，卡片那一层必须仍由 data-plan-* 属性选中。
    // 只看真正的选择器行（含 `{`），跳过注释里提到 :nth-child 的说明文字。
    const selectorLines = css
      .split('\n')
      .filter((line) => line.includes(':nth-child') && line.includes('{'))

    expect(selectorLines.length).toBeGreaterThan(0)
    for (const line of selectorLines) {
      expect(line).toMatch(/\[data-plan-/)
    }
  })

  it('keeps the skin variable channels', () => {
    const css = readFileSync(themePath, 'utf8')
    // 六条通道 + surface：暗色模式的明暗差异塞在 :root / .dark 的继承变量上，
    // 规则本身保持 (0,2,0) 特异性。
    expect(css).toContain('--aicat-skin')
    expect(css).toContain('--aicat-skin-surface')
  })

  it('does not use .dark-prefixed plan-platform rules', () => {
    const css = readFileSync(themePath, 'utf8')
    // `.dark [data-plan-platform]` 的特异性是 (0,3,0)，会盖掉金卡/银卡卡面自身的
    // (0,2,0) 规则，导致暗色下香槟金/铂银被平台色刷掉。明暗差异必须走继承变量。
    expect(css).not.toMatch(/\.dark\s+\[data-plan-platform\]/)
  })
})
