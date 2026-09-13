import { onUnmounted, ref } from 'vue'
import { parseTime } from '@/utils/format'

// 共享时钟 hook：每 interval 毫秒刷新当前时间（候补确认倒计时复用）。
export function useNow(intervalMs = 1000) {
  const now = ref(new Date())
  const timer = setInterval(() => {
    now.value = new Date()
  }, intervalMs)
  onUnmounted(() => clearInterval(timer))
  return now
}

// 距截止时间的倒计时文本 mm:ss / hh:mm:ss；已逾期返回“已逾期”。
// 截止时间为带时区的绝对时刻（RFC3339 UTC），与浏览器所处时区无关。
export function countdownText(expiresAt?: string | null, now: Date = new Date()): string {
  const deadline = parseTime(expiresAt)
  if (!deadline) return ''
  const remain = deadline.getTime() - now.getTime()
  if (remain <= 0) return '已逾期'
  const total = Math.floor(remain / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${pad(h)}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`
}
