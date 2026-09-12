<template>
  <div v-loading="loading">
    <el-alert v-if="!auth.isLoggedIn" title="请先登录后填写反馈问卷" type="warning" :closable="false" />
    <template v-else>
      <el-alert v-if="!exists" title="该活动暂无反馈问卷" type="info" :closable="false" />

      <template v-else-if="detail">
        <el-alert
          v-if="detail.submitted"
          :title="`您已于 ${formatDateTime(detail.submitted_at)} 提交问卷，提交后不能重复填写或修改`"
          type="success"
          :closable="false"
          class="mb-3"
        />
        <el-alert
          v-else-if="!detail.checked_in"
          title="仅已签到的参加者可以填写反馈问卷，您当前未签到"
          type="warning"
          :closable="false"
          class="mb-3"
        />

        <el-descriptions v-if="detail.submitted" :column="1" border class="mb-3">
          <el-descriptions-item v-for="q in detail.questions" :key="q.id" :label="q.title">
            <template v-if="answerText(q.id)">{{ answerText(q.id) }}</template>
            <span v-else class="empty-text">未填写</span>
          </el-descriptions-item>
        </el-descriptions>

        <el-form v-else-if="detail.checked_in" label-position="top" class="form">
          <el-form-item v-for="q in detail.questions" :key="q.id" :required="q.required">
            <template #label>
              <span class="q-title">{{ q.title }}</span>
              <el-tag size="small" class="q-type">{{ FeedbackQuestionTypeText[q.question_type] }}</el-tag>
            </template>

            <el-radio-group v-if="q.question_type === 'radio'" v-model="form[q.id].values[0]">
              <el-radio v-for="opt in q.options" :key="opt" :value="opt" class="block">{{ opt }}</el-radio>
            </el-radio-group>

            <el-checkbox-group v-else-if="q.question_type === 'checkbox'" v-model="form[q.id].values">
              <el-checkbox v-for="opt in q.options" :key="opt" :value="opt" class="block">{{ opt }}</el-checkbox>
            </el-checkbox-group>

            <el-input
              v-else
              v-model="form[q.id].content"
              type="textarea"
              :rows="3"
              maxlength="2000"
              show-word-limit
              placeholder="请输入您的回答"
            />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="store.submitting" @click="submit">提交问卷</el-button>
          </el-form-item>
        </el-form>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useFeedbackStore } from '@/stores/feedbackStore'
import { useAuth } from '@/hooks/useAuth'
import { FeedbackQuestionTypeText } from '@/constants/feedback'
import { formatDateTime } from '@/utils/dateFormat'
import type { FeedbackQuestion } from '@/types'

const props = defineProps<{ activityId: number }>()

const auth = useAuth()
const store = useFeedbackStore()
const detail = computed(() => store.detail)
const loading = ref(false)
const exists = ref(false)
// 每道题的作答：question_id -> { values(单选/多选), content(文本) }
const form = reactive<Record<number, { values: string[]; content: string }>>({})

function initForm(questions: FeedbackQuestion[]) {
  questions.forEach((q) => {
    form[q.id] = { values: [], content: '' }
  })
}

async function load() {
  if (!auth.isLoggedIn.value) return
  loading.value = true
  store.reset()
  exists.value = false
  try {
    const detail = await store.fetchDetail(props.activityId)
    exists.value = true
    if (detail && !detail.submitted) {
      initForm(detail.questions)
    }
  } catch {
    // 问卷不存在或未发布：静默处理，展示“暂无问卷”
    exists.value = false
  } finally {
    loading.value = false
  }
}

function answerText(questionId: number): string {
  const a = store.detail?.answers?.find((item) => item.question_id === questionId)
  if (!a) return ''
  return [...(a.values || []), a.content].filter(Boolean).join('；')
}

async function submit() {
  const questions = store.detail?.questions || []
  for (const q of questions) {
    const ans = form[q.id]
    const empty = q.question_type === 'text' ? !ans.content.trim() : ans.values.length === 0
    if (q.required && empty) {
      ElMessage.warning(`请完成题目：${q.title}`)
      return
    }
  }
  const answers = questions.map((q) => {
    const ans = form[q.id]
    return q.question_type === 'text'
      ? { question_id: q.id, content: ans.content.trim() }
      : { question_id: q.id, values: ans.values }
  })
  await store.submit(props.activityId, answers)
  ElMessage.success('问卷提交成功，感谢您的反馈')
}

onMounted(load)
</script>

<style scoped>
.mb-3 { margin-bottom: 16px; }
.form { max-width: 640px; }
.q-title { font-weight: 600; margin-right: 8px; }
.q-type { margin-left: 4px; }
.block { display: flex; }
.empty-text { color: #909399; }
</style>
