import request from '@/utils/request'
import type { FeedbackQuestionTypeValue } from '@/constants/feedback'

export interface FeedbackQuestionInput {
  question_type: FeedbackQuestionTypeValue
  title: string
  options: string[]
  required: boolean
}

export interface FeedbackCreatePayload {
  title?: string
  description?: string
  questions: FeedbackQuestionInput[]
}

export interface FeedbackAnswerInput {
  question_id: number
  values?: string[]
  content?: string
}

// 组织者为已结束活动创建问卷（草稿态）
export function createFeedback(activityId: number, data: FeedbackCreatePayload) {
  return request.post(`/activities/${activityId}/feedback`, data)
}

// 组织者发布问卷
export function publishFeedback(activityId: number) {
  return request.post(`/activities/${activityId}/feedback/publish`)
}

// 参加者查看问卷及本人提交状态；silent=true 时失败不弹全局提示（用于探测）
export function getFeedback(activityId: number, silent = false) {
  return request.get(`/activities/${activityId}/feedback`, { skipErrorMessage: silent })
}

// 参加者提交问卷
export function submitFeedback(activityId: number, answers: FeedbackAnswerInput[]) {
  return request.post(`/activities/${activityId}/feedback/submit`, { answers })
}

// 组织者查看填写统计；silent=true 时失败不弹全局提示（用于探测问卷是否存在）
export function getFeedbackStats(activityId: number, silent = false) {
  return request.get(`/activities/${activityId}/feedback/stats`, { skipErrorMessage: silent })
}
