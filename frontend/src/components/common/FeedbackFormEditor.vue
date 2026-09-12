<template>
  <el-form label-position="top">
    <el-form-item label="问卷标题（选填，默认使用活动名称）">
      <el-input v-model="form.title" maxlength="200" placeholder="例如：《活动名称》活动反馈问卷" />
    </el-form-item>
    <el-form-item label="问卷说明（选填）">
      <el-input v-model="form.description" type="textarea" :rows="2" maxlength="2000" show-word-limit />
    </el-form-item>

    <div class="q-header">
      <span class="q-header-title">题目列表（{{ form.questions.length }}）</span>
      <el-button size="small" @click="addQuestion(FeedbackQuestionType.RADIO)">添加单选题</el-button>
      <el-button size="small" @click="addQuestion(FeedbackQuestionType.CHECKBOX)">添加多选题</el-button>
      <el-button size="small" @click="addQuestion(FeedbackQuestionType.TEXT)">添加文本题</el-button>
    </div>

    <el-card v-for="(q, index) in form.questions" :key="index" class="q-card" shadow="never">
      <div class="q-card-head">
        <el-tag>{{ FeedbackQuestionTypeText[q.question_type] }}</el-tag>
        <el-switch v-model="q.required" active-text="必答" inline-prompt />
        <el-button size="small" type="danger" link @click="removeQuestion(index)">删除题目</el-button>
      </div>
      <el-form-item label="题目标题" class="mt-2">
        <el-input v-model="q.title" maxlength="255" placeholder="请输入题目标题" />
      </el-form-item>
      <el-form-item v-if="q.question_type !== 'text'" label="选项（每行一个，至少 2 个，不能重复）">
        <el-input
          v-model="optionText[index]"
          type="textarea"
          :rows="3"
          placeholder="非常满意&#10;满意&#10;一般"
          @input="syncOptions(index)"
        />
      </el-form-item>
    </el-card>

    <el-alert v-if="error" :title="error" type="error" :closable="false" class="mt-2" />
  </el-form>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { FeedbackQuestionType, FeedbackQuestionTypeText } from '@/constants/feedback'
import type { FeedbackQuestionInput } from '@/api/feedback'
import type { FeedbackQuestionTypeValue } from '@/constants/feedback'

interface EditableQuestion {
  question_type: FeedbackQuestionTypeValue
  title: string
  options: string[]
  required: boolean
}

const form = reactive<{ title: string; description: string; questions: EditableQuestion[] }>({
  title: '',
  description: '',
  questions: [],
})
// 选项以多行文本编辑，提交前拆分为数组
const optionText = reactive<Record<number, string>>({})
const error = ref('')

function addQuestion(type: FeedbackQuestionTypeValue) {
  form.questions.push({ question_type: type, title: '', options: [], required: true })
}

function removeQuestion(index: number) {
  form.questions.splice(index, 1)
  // 重建文本映射的索引
  const values = Object.values(optionText)
  Object.keys(optionText).forEach((k) => delete optionText[Number(k)])
  values.forEach((v, i) => (optionText[i] = v))
}

function syncOptions(index: number) {
  form.questions[index].options = (optionText[index] || '')
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
}

// validate 校验并返回创建问卷所需载荷；失败时设置错误提示并返回 null。
function validate(): { title: string; description: string; questions: FeedbackQuestionInput[] } | null {
  error.value = ''
  if (form.questions.length === 0) {
    error.value = '请至少添加一道题目'
    return null
  }
  const seen = new Set<string>()
  const questions: FeedbackQuestionInput[] = []
  for (let i = 0; i < form.questions.length; i++) {
    const q = form.questions[i]
    if (!q.title.trim()) {
      error.value = `第 ${i + 1} 题的标题不能为空`
      return null
    }
    if (q.question_type !== 'text') {
      syncOptions(i)
      if (q.options.length < 2) {
        error.value = `第 ${i + 1} 题至少需要 2 个选项`
        return null
      }
      q.options.forEach((opt) => seen.add(opt))
      if (seen.size !== q.options.length) {
        error.value = `第 ${i + 1} 题存在重复选项`
        return null
      }
      seen.clear()
    }
    questions.push({
      question_type: q.question_type,
      title: q.title.trim(),
      options: q.question_type === 'text' ? [] : q.options,
      required: q.required,
    })
  }
  return { title: form.title.trim(), description: form.description.trim(), questions }
}

defineExpose({ validate })
</script>

<style scoped>
.q-header { display: flex; align-items: center; gap: 8px; margin: 8px 0 12px; }
.q-header-title { font-weight: 600; margin-right: auto; }
.q-card { margin-bottom: 12px; }
.q-card-head { display: flex; align-items: center; gap: 12px; }
.q-card-head .el-button { margin-left: auto; }
.mt-2 { margin-top: 12px; }
</style>
