// 共享格式化工具

// parseTime 解析后端时间：候补接口下发带时区的 RFC3339（UTC，Z 结尾）绝对时刻；
// 旧接口的“2006-01-02 15:04:05”无时区墙上时间保持原样返回字符串以兼容。
export function parseTime(s?: string | null): Date | null {
  if (!s) return null

  // 带时区信息（Z 或 +08:00）：按绝对时刻解析，浏览器自动按本地时区展示
  if (/[zZ]$/.test(s) || /[+-]\d{2}:?\d{2}$/.test(s)) {
    const d = new Date(s)
    return Number.isNaN(d.getTime()) ? null : d
  }

  // 无时区的旧格式 yyyy-MM-dd HH:mm:ss：保持原行为，不当成本地时间错解；
  // 越界字段（如 13 月、99 点）判为非法，避免 Date 自动进位产生错误倒计时。
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?)?$/)
  if (m) {
    const num = (v: string | undefined) => (v === undefined ? undefined : Number(v))
    const [sy, mo, dy] = [Number(m[1]), Number(m[2]), Number(m[3])]
    const hh = num(m[4])
    const mi = num(m[5])
    const ss = num(m[6])
    const inRange = (v: number | undefined, lo: number, hi: number) => v === undefined || (v >= lo && v <= hi)
    if (!inRange(mo, 1, 12) || !inRange(dy, 1, 31) || !inRange(hh, 0, 23) || !inRange(mi, 0, 59) || !inRange(ss, 0, 59)) {
      return null
    }
    const d = new Date(sy, mo - 1, dy, hh ?? 0, mi ?? 0, ss ?? 0)
    return Number.isNaN(d.getTime()) ? null : d
  }
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? null : d
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

// formatDateTime 统一按浏览器本地时区显示“yyyy-MM-dd HH:mm:ss”。
export function formatDateTime(s?: string | null): string {
  const d = parseTime(s)
  if (!d) return '-'
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

// formatDateTimeShort 显示“yyyy-MM-dd HH:mm”（截止时间展示用）。
export function formatDateTimeShort(s?: string | null): string {
  const d = parseTime(s)
  if (!d) return '-'
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function formatDate(s?: string | null): string {
  if (!s) return '-'
  return s.slice(0, 10)
}

export function formatWeight(w?: number | null): string {
  if (w === undefined || w === null) return '-'
  return `${w.toFixed(2)} kg`
}

export function formatArea(a?: number | null): string {
  if (a === undefined || a === null) return '-'
  return `${a.toFixed(1)} m²`
}

export function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}
