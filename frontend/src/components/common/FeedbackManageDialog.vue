<template>
  <el-dialog
    :model-value="modelValue"
    title="反馈问卷管理"
    width="760px"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
    @open="onOpen"
  >
    <div v-loading="loading">
      <!-- 未创建问卷：编辑并创建 -->
      <template v-if="mode === 'create'">
        <FeedbackFormEditor ref="editorRef" />
      </template>

      <!-- 已创建（草稿/已发布）：状态与统计 -->
      <template v-else-if="stats">
        <div class="status-bar">
          <el-tag :type="stats.survey.status === 'published' ? 'success' : 'warning'">
            {{ stats.survey.status === 'published' ? '已发布' : '草稿（参加者暂不可见）' }}
          </el-tag>
          <el-button v-if="stats.survey.status === 'draft'" type="primary" size="small" @click="publish">
            发布问卷
          </el-button>
        </div>
        <FeedbackStats ref="statsRef" :activity-id="activityId" />
      </template>
    </div>

    <template #footer>
      <el-button @click="emit('update:modelValue', false)">关闭</el-button>
      <el-button v-if="mode === 'create'" type="primary" :loading="saving" @click="create">创建问卷</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { ElMessage } from 'element-plus'
import FeedbackFormEditor from './FeedbackFormEditor.vue'
import FeedbackStats from './FeedbackStats.vue'
import { createFeedback, getFeedbackStats, publishFeedback } from '@/api/feedback'
import type { FeedbackStats as FeedbackStatsData } from '@/types'

const props = defineProps<{ modelValue: boolean; activityId: number }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>()

const loading = ref(false)
const saving = ref(false)
const mode = ref<'create' | 'existing'>('create')
const stats = ref<FeedbackStatsData | null>(null)
const editorRef = ref<InstanceType<typeof FeedbackFormEditor>>()
const statsRef = ref<InstanceType<typeof FeedbackStats>>()

async function onOpen() {
  mode.value = 'create'
  stats.value = null
  loading.value = true
  try {
    const res = await getFeedbackStats(props.activityId, true)
    stats.value = res.data
    mode.value = 'existing'
  } catch {
    // 404：问卷尚未创建，进入创建模式（静默探测）
    mode.value = 'create'
  } finally {
    loading.value = false
  }
}

async function create() {
  const payload = editorRef.value?.validate()
  if (!payload) return
  saving.value = true
  try {
    await createFeedback(props.activityId, payload)
    ElMessage.success('问卷已创建（草稿），发布后参加者即可填写')
    await reloadStats()
  } finally {
    saving.value = false
  }
}

async function publish() {
  await publishFeedback(props.activityId)
  ElMessage.success('问卷已发布')
  await reloadStats()
  await nextTick()
  statsRef.value?.load()
}

async function reloadStats() {
  const res = await getFeedbackStats(props.activityId)
  stats.value = res.data
  mode.value = 'existing'
}
</script>

<style scoped>
.status-bar { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
</style>
