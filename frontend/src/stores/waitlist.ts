import { defineStore } from 'pinia'
import {
  cancelWaitlist,
  confirmWaitlist,
  expireWaitlist,
  joinWaitlist,
  listAllWaitlist,
  listMyWaitlist,
  removeWaitlist,
  type WaitlistEntry
} from '@/api/waitlist'

interface WaitlistState {
  mine: WaitlistEntry[]
  mineTotal: number
  queue: WaitlistEntry[]
  queueTotal: number
  loading: boolean
}

// 候补 store：我的候补页与管理端队列页共用同一 store 的不同状态切片。
export const useWaitlistStore = defineStore('waitlist', {
  state: (): WaitlistState => ({ mine: [], mineTotal: 0, queue: [], queueTotal: 0, loading: false }),
  actions: {
    async fetchMine(params?: Record<string, any>) {
      this.loading = true
      try {
        const data = await listMyWaitlist(params)
        this.mine = data.list
        this.mineTotal = data.total
      } finally {
        this.loading = false
      }
    },
    async fetchQueue(params?: Record<string, any>) {
      this.loading = true
      try {
        const data = await listAllWaitlist(params)
        this.queue = data.list
        this.queueTotal = data.total
      } finally {
        this.loading = false
      }
    },
    async join(plotId: number, remark?: string) {
      return joinWaitlist(plotId, remark)
    },
    async confirm(id: number) {
      return confirmWaitlist(id)
    },
    async cancel(id: number) {
      return cancelWaitlist(id)
    },
    async remove(id: number, remark?: string) {
      return removeWaitlist(id, remark)
    },
    async expire(id: number) {
      return expireWaitlist(id)
    }
  }
})
