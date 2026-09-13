<template>
  <div class="page-card">
    <div style="display: flex; justify-content: space-between; align-items: center">
      <h3 class="page-title">我的候补</h3>
      <el-radio-group v-model="scope" size="small" @change="onScopeChange">
        <el-radio-button label="active">进行中</el-radio-button>
        <el-radio-button label="all">全部记录</el-radio-button>
      </el-radio-group>
    </div>

    <el-alert
      v-if="invitedCount > 0"
      type="error"
      :closable="false"
      show-icon
      style="margin: 12px 0"
      :title="`你有 ${invitedCount} 个地块已轮到确认，请在截止时间前点击「确认认养」，逾期将自动顺延给下一位`"
    />

    <DataTable :data="store.mine" :loading="store.loading" :total="store.mineTotal" :page-size="pagination.size.value" :current-page="pagination.page.value" @update:current-page="onPage">
      <el-table-column label="地块" min-width="160">
        <template #default="{ row }">
          <div>{{ row.plot?.name || '-' }}</div>
          <div style="color: #909399; font-size: 12px">{{ row.plot?.code }}</div>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><StatusBadge :value="row.status" :meta-map="WaitlistStatusMeta" /></template>
      </el-table-column>
      <el-table-column label="排队位置" width="100" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.status === 'waiting'" type="info" effect="plain">第 {{ row.position }} 位</el-tag>
          <el-tag v-else-if="row.status === 'invited'" type="danger">队首 · 请确认</el-tag>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="登记时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.registered_at) }}</template>
      </el-table-column>
      <el-table-column label="确认截止时间" width="240">
        <template #default="{ row }">
          <template v-if="row.status === 'invited'">
            <div style="color: #f56c6c; font-weight: 600">{{ formatDateTime(row.confirm_expires_at) }}</div>
            <div style="font-size: 12px">剩余 <span :style="{ color: remainText(row.confirm_expires_at) === '已逾期' ? '#909399' : '#f56c6c' }">{{ remainText(row.confirm_expires_at) }}</span></div>
          </template>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column prop="remark" label="备注" min-width="120" show-overflow-tooltip />
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.status === 'invited'" type="success" size="small" :loading="actingId === row.id" @click="confirm(row)">确认认养</el-button>
          <el-button v-if="row.status === 'waiting' || row.status === 'invited'" type="danger" plain size="small" :loading="actingId === row.id" @click="cancel(row)">放弃候补</el-button>
        </template>
      </el-table-column>
    </DataTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useWaitlistStore } from '@/stores/waitlist'
import { usePagination } from '@/hooks/usePagination'
import { countdownText, useNow } from '@/hooks/useNow'
import DataTable from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { WaitlistStatusMeta } from '@/constants'
import { formatDateTime } from '@/utils/format'
import type { WaitlistEntry } from '@/api/waitlist'

const store = useWaitlistStore()
const router = useRouter()
const pagination = usePagination()
const now = useNow(1000)
const scope = ref<'active' | 'all'>('active')
const actingId = ref(0)

const invitedCount = computed(() => store.mine.filter((e) => e.status === 'invited').length)

function remainText(expiresAt?: string) {
  return countdownText(expiresAt, now.value)
}

async function fetch() {
  const params: Record<string, any> = { page: pagination.page.value, page_size: pagination.size.value }
  if (scope.value === 'all') params.status = 'all'
  await store.fetchMine(params)
}

function onPage(page: number) {
  pagination.page.value = page
  fetch()
}

function onScopeChange() {
  pagination.page.value = 1
  fetch()
}

async function confirm(row: WaitlistEntry) {
  try {
    await ElMessageBox.confirm(`确认认养地块 ${row.plot?.name}（${row.plot?.code}）吗？确认后地块将认养到你名下。`, '队首确认认养', {
      confirmButtonText: '确认认养',
      cancelButtonText: '再想想',
      type: 'success'
    })
  } catch {
    return
  }
  actingId.value = row.id
  try {
    await store.confirm(row.id)
    ElMessage.success('确认成功，地块已认养到你名下')
    await fetch()
    router.push('/plots')
  } finally {
    actingId.value = 0
  }
}

async function cancel(row: WaitlistEntry) {
  try {
    await ElMessageBox.confirm(`确定放弃地块 ${row.plot?.name} 的候补吗？${row.status === 'invited' ? '放弃后将自动顺延给下一位。' : ''}`, '放弃候补', {
      confirmButtonText: '放弃',
      cancelButtonText: '取消',
      type: 'warning'
    })
  } catch {
    return
  }
  actingId.value = row.id
  try {
    await store.cancel(row.id)
    ElMessage.success('已放弃该地块候补')
    await fetch()
  } finally {
    actingId.value = 0
  }
}

onMounted(fetch)
</script>
