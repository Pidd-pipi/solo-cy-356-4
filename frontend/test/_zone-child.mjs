// 子进程脚本：在指定 TZ 下加载真实模块并跑该时区的表驱动断言。
// 仅由 time.test.mjs 以子进程方式调用，不是测试文件本身。
// 用法：node _zone-child.mjs <bundleUrl> <TZ>
// 成功退出 0；失败打印首个错误并退出 1。
;(async () => {
  const [, , bundleUrl, tz] = process.argv
  process.env.TZ = tz
  const { formatDateTime, countdownText } = await import(bundleUrl)

  // 各时区下的墙上时间显示断言（倒计时为绝对时刻差，与时区无关，每组都校验）。
  const now = new Date('2026-09-13T02:00:00Z')
  const casesByZone = {
    'Asia/Shanghai': [
      ['截止绝对时刻按 +8 显示', formatDateTime('2026-09-13T02:30:00Z'), '2026-09-13 10:30:00'],
      ['登记绝对时刻按 +8 显示', formatDateTime('2026-09-13T01:00:00Z'), '2026-09-13 09:00:00'],
      ['旧版无时区文本按本地墙上时间兼容', formatDateTime('2026-09-13 10:30:00'), '2026-09-13 10:30:00']
    ],
    'America/Sao_Paulo': [
      // UTC-3：02:30 UTC 为前一天 23:30（跨日）
      ['截止绝对时刻按 -3 显示（跨日）', formatDateTime('2026-09-13T02:30:00Z'), '2026-09-12 23:30:00'],
      ['登记绝对时刻按 -3 显示（跨日）', formatDateTime('2026-09-13T01:00:00Z'), '2026-09-12 22:00:00'],
      ['旧版无时区文本按本地墙上时间兼容', formatDateTime('2026-09-12 23:30:00'), '2026-09-12 23:30:00']
    ],
    'Pacific/Honolulu': [
      // UTC-10：02:30 UTC 为前一天 16:30（负偏移且跨日）
      ['截止绝对时刻按 -10 显示（跨日）', formatDateTime('2026-09-13T02:30:00Z'), '2026-09-12 16:30:00']
    ]
  }

  const cases = [
    ...(casesByZone[tz] || []),
    ['倒计时基于绝对时刻（与页面时区无关，30 分钟）', countdownText('2026-09-13T02:30:00Z', now), '30:00']
  ]

  for (const [name, got, want] of cases) {
    if (got !== want) {
      console.error(`[${tz}] ${name}: got=${JSON.stringify(got)} want=${JSON.stringify(want)}`)
      process.exit(1)
    }
  }
  process.exit(0)
})().catch((e) => {
  console.error(`[${process.argv[3] || '?'}] child crashed: ${e?.stack || e}`)
  process.exit(1)
})
