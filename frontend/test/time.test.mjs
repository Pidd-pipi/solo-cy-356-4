// 页面时间自动化测试（Node 20 内置 node:test，无需引入测试框架）。
//
// 被测对象为真实的 src/utils/format.ts 与 src/hooks/useNow.ts，通过 esbuild 即时
// 打包为 ESM 后 import。跨时区显示用独立子进程设置 TZ（主进程 TZ 无法运行中切换）。
//
// 运行：npm run test:time
import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join } from 'node:path'
import { rmSync } from 'node:fs'
import { build } from 'esbuild'

const here = dirname(fileURLToPath(import.meta.url))
const entry = join(here, 'tz-entry.ts')
const bundle = join(here, '.tz-bundle.mjs')

// 先即时打包真实 TS 模块，再导入（顶层 await 先于所有用例执行）。
await build({ entryPoints: [entry], bundle: true, format: 'esm', outfile: bundle, logLevel: 'silent' })
const { formatDateTime, formatDateTimeShort, formatDate, parseTime, countdownText } = await import(
  pathToFileURL(bundle).href
)
test.after(() => {
  try {
    rmSync(bundle)
  } catch {
    /* ignore */
  }
})

// 上海时区基准：UTC 02:00（与页面所处时区无关的绝对“现在”）
const NOW = new Date('2026-09-13T02:00:00Z')

test('上海时区显示：UTC 绝对时刻按 +8 渲染墙上时间', async (t) => {
  const out = runInZone('Asia/Shanghai')
  assert.equal(out.status, 0, out.stderr)
})

test('负偏移时区显示：UTC-3 / UTC-10 正确（含跨日）', async (t) => {
  for (const tz of ['America/Sao_Paulo', 'Pacific/Honolulu']) {
    await t.test(tz, () => {
      const out = runInZone(tz)
      assert.equal(out.status, 0, out.stderr)
    })
  }
})

test('旧版无时区文本兼容：yyyy-MM-dd HH:mm:ss 按浏览器本地墙上时间解析', () => {
  // 该断言在任意 TZ 下都成立：输入数字原样作为本地墙上时间显示
  assert.equal(formatDateTime('2026-09-13 10:30:45'), '2026-09-13 10:30:45')
  assert.equal(formatDateTime('2026-09-13T08:05:00'), '2026-09-13 08:05:00')
  assert.equal(formatDate('2026-09-13 10:30:45'), '2026-09-13')
})

test('空值：不显示错误内容，倒计时为空串而非“已逾期”', () => {
  assert.equal(formatDateTime(''), '-')
  assert.equal(formatDateTime(null), '-')
  assert.equal(formatDateTime(undefined), '-')
  assert.equal(countdownText('', NOW), '')
  assert.equal(countdownText(null, NOW), '')
  assert.equal(countdownText(undefined, NOW), '')
})

test('非法值：不抛异常、不显示错误倒计时', () => {
  for (const bad of [
    'not-a-date',
    '2026-13-40 99:99:99',
    '9999-99-99',
    '2026-02-31 10:00:00', // 2 月没有 31 日（会进位到 3 月）
    '2026-04-31 10:00:00', // 4 月只有 30 天
    '2026-02-29 10:00:00', // 平年无 2-29（会进位到 3-1）
    '1900-02-29 10:00:00' // 1900 是平年（百年不闰）
  ]) {
    assert.equal(parseTime(bad), null, `parseTime(${bad}) 应为 null`)
    assert.equal(formatDateTime(bad), '-', `formatDateTime(${bad}) 应为 -`)
    // 无法解析出截止时间 → 倒计时留空，而不是被当成过去时间显示“已逾期”
    assert.equal(countdownText(bad, NOW), '', `countdownText(${bad}) 应为空串`)
  }
})

test('不存在的月日组合返回无效，合法闰日保留', () => {
  // 非法组合（不同月份边界）
  for (const bad of [
    '2026-02-30',
    '2026-02-31',
    '2026-02-29', // 2026 平年
    '2026-04-31',
    '2026-06-31',
    '2026-09-31',
    '2026-11-31',
    '1900-02-29' // 平世纪年
  ]) {
    assert.equal(parseTime(bad), null, `${bad} 不存在，应解析为 null`)
  }

  // 合法闰日：2024（能被 4 整除且非整百年）、2000（400 年闰）必须保留
  const leap = parseTime('2024-02-29 10:30:00')
  assert.ok(leap, '2024-02-29 是合法闰日')
  assert.equal(leap.getFullYear(), 2024)
  assert.equal(leap.getMonth(), 1)
  assert.equal(leap.getDate(), 29)

  const leapCentury = parseTime('2000-02-29T08:00:00')
  assert.ok(leapCentury, '2000-02-29 是合法闰日（400 年闰）')
  assert.equal(leapCentury.getMonth(), 1)
  assert.equal(leapCentury.getDate(), 29)

  // 各月大小边界合法
  for (const ok of ['2026-01-31', '2026-03-31', '2026-04-30', '2026-12-31']) {
    assert.ok(parseTime(ok), `${ok} 应合法`)
  }
})

test('剩余时间跨越一小时：hh:mm:ss 格式', () => {
  // 1 小时 5 分 3 秒
  assert.equal(countdownText('2026-09-13T03:05:03Z', NOW), '01:05:03')
  // 不足一小时：mm:ss
  assert.equal(countdownText('2026-09-13T02:59:59Z', NOW), '59:59')
  assert.equal(countdownText('2026-09-13T02:30:00Z', NOW), '30:00')
  // 刚好 1 小时
  assert.equal(countdownText('2026-09-13T03:00:00Z', NOW), '01:00:00')
})

test('已逾期边界：整 0 与超过截止都判定为已逾期', () => {
  // 超过 1 秒
  assert.equal(countdownText('2026-09-13T01:59:59Z', NOW), '已逾期')
  // 恰好等于截止时刻（remain <= 0）
  assert.equal(countdownText('2026-09-13T02:00:00Z', NOW), '已逾期')
  // 还剩 1 秒仍在确认期
  assert.equal(countdownText('2026-09-13T02:00:01Z', NOW), '00:01')
})

test('短格式时间显示', () => {
  assert.match(formatDateTimeShort('2026-09-13T02:30:00Z'), /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  assert.equal(formatDateTimeShort(''), '-')
})

function runInZone(tz) {
  try {
    execFileSync(process.execPath, [join(here, '_zone-child.mjs'), pathToFileURL(bundle).href, tz], {
      env: { ...process.env, TZ: tz },
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe']
    })
    return { status: 0, stderr: '' }
  } catch (e) {
    return { status: e.status ?? 1, stderr: String(e.stderr || e.message) }
  }
}
