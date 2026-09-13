import { get, post } from '@/utils/request'

export interface WaitlistEntry {
  id: number
  plot_id: number
  user_id: number
  status: string
  position: number
  remark: string
  user: {
    id: number
    username: string
    nickname: string
    role: string
  } | null
  plot: {
    id: number
    name: string
    code: string
    status: string
  } | null
  registered_at: string
  invited_at: string
  confirm_expires_at: string
  confirmed_at: string
  remain_seconds: number
  created_at: string
}

export interface WaitlistPage {
  list: WaitlistEntry[]
  total: number
  page: number
  page_size: number
}

// 登记候补
export function joinWaitlist(plotId: number, remark?: string): Promise<WaitlistEntry> {
  return post(`/plots/${plotId}/waitlist`, remark ? { remark } : {})
}

// 我的候补（status 默认仅有效记录，传 all 查全部历史）
export function listMyWaitlist(params?: Record<string, any>): Promise<WaitlistPage> {
  return get('/waitlist/mine', { params })
}

// 队首确认认养
export function confirmWaitlist(id: number): Promise<{ waitlist: WaitlistEntry; plot: any }> {
  return post(`/waitlist/${id}/confirm`)
}

// 主动放弃候补
export function cancelWaitlist(id: number): Promise<WaitlistEntry> {
  return post(`/waitlist/${id}/cancel`)
}

// 管理端：候补队列（可按 plot_id / status 过滤）
export function listAllWaitlist(params?: Record<string, any>): Promise<WaitlistPage> {
  return get('/waitlist', { params })
}

// 管理端：移除候选
export function removeWaitlist(id: number, remark?: string): Promise<WaitlistEntry> {
  return post(`/waitlist/${id}/remove`, remark ? { remark } : {})
}

// 管理端：处理逾期异常（置 expired 并顺延）
export function expireWaitlist(id: number): Promise<{ waitlist: WaitlistEntry; advanced_to: number }> {
  return post(`/waitlist/${id}/expire`)
}
