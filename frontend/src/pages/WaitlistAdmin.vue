<template>
  <div class="page-card">
    <div style="display: flex; justify-content: space-between; align-items: center">
      <h3 class="page-title">候补队列管理（管理员）</h3>
    </div>

    <el-card shadow="never" style="margin-bottom: 16px">
      <el-form inline>
        <el-form-item label="地块 ID">
          <el-input v-model="plotIdFilter" placeholder="如 3，留空查全部" clearable style="width: 180px" @keyup.enter="onSearch" />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="statusFilter" placeholder="全部" clearable style="width: 150px">
            <el-option v-for="(meta, key) in WaitlistStatusMeta" :key="key" :label="meta.label" :value="key" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="onSearch">查询</el-button>
          <el-button @click="onReset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <DataTable :data="store.queue" :loading="store.loading" :total="store.queueTotal" :page-size="pagination.size.value" :current-page="pagination.page.value" @update:current-page="onPage">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column label="地块" min-width="150">
        <template #default="{ row }">
          <div>{{ row.plot?.name || '-' }}</div>
          <div style="color: #909399; font-size: 12px">{{ row.plot?.code }}（地块状态：{{ PlotStatusMeta[row.plot?.status]?.label || row.plot?.status }}）</div>
        </template>
      </el-table-column>
      <el-table-column label="候选用户" width="150">
        <template #default="{ row }">{{ row.user?.nickname || row.user?.username }} <span style="color:#909399">#{{ row.user_id }}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><StatusBadge :value="row.status" :meta-map="WaitlistStatusMeta" /></template>
      </el-table-column>
      <el-table-column label="排队位置" width="90" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.position > 0" type="info" effect="plain">第 {{ row.position }} 位</el-tag>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="登记时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.registered_at) }}</template>
      </el-table-column>
      <el-table-column label="确认截止" width="190">
        <template #default="{ row }">
          <template v-if="row.status === 'invited'">
            <span :style="{ color: isOverdue(row.confirm_expires_at) ? '#e6a23c' : '#f56c6c' }">{{ formatDateTime(row.confirm_expires_at) }}</span>
            <el-tag v-if="isOverdue(row.confirm_expires_at)" type="warning" size="small" style="margin-left: 4px">已逾期</el-tag>
          </template>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column prop="remark" label="备注" min-width="120" show-overflow-tooltip />
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <template v-if="row.status === 'waiting' || row.status === 'invited'">
            <el-popconfirm
              v-if="row.status === 'invited' && isOverdue(row.confirm_expires_at)"
              title="确认将该逾期候选置为过期并顺延给下一位？"
              confirm-button-text="顺延"
              cancel-button-text="取消"
              @confirm="doExpire(row)"
            >
              <template #reference>
                <el-button type="warning" size="small">处理逾期</el-button>
              </template>
            </el-popconfirm>
            <el-popconfirm title="确认移除该候选？若为队首将自动顺延下一位。" confirm-button-text="移除" cancel-button-text="取消" @confirm="doRemove(row)">
              <template #reference>
                <el-button type="danger" plain size="small">移除候选</el-button>
              </template>
            </el-popconfirm>
          </template>
          <span v-else style="color: #c0c4cc">已处理</span>
        </template>
      </el-table-column>
    </DataTable>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useWaitlistStore } from '@/stores/waitlist'
import { usePagination } from '@/hooks/usePagination'
import { countdownText, useNow } from '@/hooks/useNow'
import DataTable from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { PlotStatusMeta, WaitlistStatusMeta } from '@/constants'
import { formatDateTime } from '@/utils/format'
import type { WaitlistEntry } from '@/api/waitlist'

const store = useWaitlistStore()
const pagination = usePagination()
const now = useNow(1000)
const plotIdFilter = ref('')
const statusFilter = ref('')

function isOverdue(expiresAt?: string) {
  return countdownText(expiresAt, now.value) === '已逾期'
}

async function fetch() {
  const params: Record<string, any> = { page: pagination.page.value, page_size: pagination.size.value }
  if (plotIdFilter.value.trim()) params.plot_id = plotIdFilter.value.trim()
  if (statusFilter.value) params.status = statusFilter.value
  await store.fetchQueue(params)
}

function onPage(page: number) {
  pagination.page.value = page
  fetch()
}

function onSearch() {
  pagination.page.value = 1
  fetch()
}

function onReset() {
  plotIdFilter.value = ''
  statusFilter.value = ''
  pagination.page.value = 1
  fetch()
}

async function doRemove(row: WaitlistEntry) {
  await store.remove(row.id)
  ElMessage.success('候选已移出候补队列')
  await fetch()
}

async function doExpire(row: WaitlistEntry) {
  const res = await store.expire(row.id)
  ElMessage.success(res.advanced_to > 0 ? '逾期候选已顺延，下一位进入确认期' : '逾期候选已处理，地块回到空闲池')
  await fetch()
}

onMounted(fetch)
</script>
