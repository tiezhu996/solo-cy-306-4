<template>
  <div v-loading="loading">
    <template v-if="stats">
      <div class="head">
        <div>
          <h3 class="title">{{ stats.survey.title }}</h3>
          <el-tag :type="stats.survey.status === 'published' ? 'success' : 'warning'">
            {{ stats.survey.status === 'published' ? '已发布' : '草稿' }}
          </el-tag>
        </div>
        <el-button size="small" @click="load">刷新统计</el-button>
      </div>
      <el-descriptions :column="2" border class="mb-3">
        <el-descriptions-item label="填写人数">{{ stats.response_count }}</el-descriptions-item>
        <el-descriptions-item label="题目数量">{{ stats.questions.length }}</el-descriptions-item>
      </el-descriptions>

      <el-empty v-if="stats.response_count === 0" description="暂无参加者提交" />

      <el-card v-for="qs in stats.question_stats" :key="qs.question_id" class="q-card" shadow="never">
        <div class="q-title">
          <span>{{ qs.title }}</span>
          <el-tag size="small">{{ FeedbackQuestionTypeText[qs.question_type] }}</el-tag>
          <span class="answered">已答 {{ qs.answered_count }} 人</span>
        </div>

        <template v-if="qs.question_type === 'text'">
          <el-empty v-if="!qs.texts || qs.texts.length === 0" description="暂无文本回答" :image-size="60" />
          <ul v-else class="text-list">
            <li v-for="(t, i) in qs.texts" :key="i" class="text-item">
              <span class="text-content">{{ t.content }}</span>
              <span class="text-time">{{ t.created_at }}</span>
            </li>
          </ul>
        </template>

        <div v-else class="options">
          <div v-for="opt in qs.options" :key="opt.option" class="option-row">
            <span class="option-label">{{ opt.option }}</span>
            <el-progress
              class="option-bar"
              :percentage="percent(opt.count)"
              :stroke-width="16"
              :text-inside="true"
            />
            <span class="option-count">{{ opt.count }} 票</span>
          </div>
        </div>
      </el-card>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getFeedbackStats } from '@/api/feedback'
import { FeedbackQuestionTypeText } from '@/constants/feedback'
import type { FeedbackStats } from '@/types'

const props = defineProps<{ activityId: number }>()

const stats = ref<FeedbackStats | null>(null)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await getFeedbackStats(props.activityId)
    stats.value = res.data
  } finally {
    loading.value = false
  }
}

function percent(count: number): number {
  const total = stats.value?.response_count || 0
  if (total === 0) return 0
  return Math.round((count / total) * 100)
}

onMounted(load)
defineExpose({ load })
</script>

<style scoped>
.head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.title { margin: 0 8px 4px 0; display: inline-block; }
.mb-3 { margin-bottom: 16px; }
.q-card { margin-bottom: 12px; }
.q-title { display: flex; align-items: center; gap: 8px; font-weight: 600; margin-bottom: 10px; }
.answered { margin-left: auto; color: #909399; font-size: 12px; font-weight: 400; }
.options { display: flex; flex-direction: column; gap: 8px; }
.option-row { display: flex; align-items: center; gap: 12px; }
.option-label { width: 120px; color: #606266; flex-shrink: 0; }
.option-bar { flex: 1; }
.option-count { width: 60px; color: #606266; flex-shrink: 0; }
.text-list { list-style: none; margin: 0; padding: 0; max-height: 260px; overflow-y: auto; }
.text-item { display: flex; justify-content: space-between; gap: 12px; padding: 8px 0; border-bottom: 1px dashed #ebeef5; }
.text-content { color: #303133; }
.text-time { color: #909399; font-size: 12px; white-space: nowrap; }
</style>
