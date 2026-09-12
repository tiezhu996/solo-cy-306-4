import { defineStore } from 'pinia'
import { getFeedback, getFeedbackStats, submitFeedback } from '@/api/feedback'
import type { FeedbackAnswerInput } from '@/api/feedback'
import type { FeedbackQuestion, FeedbackSurvey, FeedbackAnswer, FeedbackStats } from '@/types'

interface FeedbackDetail {
  survey: FeedbackSurvey
  questions: FeedbackQuestion[]
  submitted: boolean
  checked_in: boolean
  can_submit: boolean
  submitted_at?: string
  answers?: FeedbackAnswer[]
}

// 问卷数据按活动 ID 缓存，切换活动或刷新页面后重新从后端拉取（数据持久化在 MySQL 中）。
export const useFeedbackStore = defineStore('feedback', {
  state: () => ({
    detail: null as FeedbackDetail | null,
    stats: null as FeedbackStats | null,
    submitting: false,
  }),
  actions: {
    async fetchDetail(activityId: number) {
      const res = await getFeedback(activityId)
      this.detail = res.data
      return this.detail
    },
    async fetchStats(activityId: number) {
      const res = await getFeedbackStats(activityId)
      this.stats = res.data
      return this.stats
    },
    async submit(activityId: number, answers: FeedbackAnswerInput[]) {
      this.submitting = true
      try {
        const res = await submitFeedback(activityId, answers)
        await this.fetchDetail(activityId)
        return res
      } finally {
        this.submitting = false
      }
    },
    reset() {
      this.detail = null
      this.stats = null
    },
  },
})
