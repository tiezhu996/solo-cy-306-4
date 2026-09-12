// 反馈问卷枚举（与后端 backend/internal/constants/feedback.go 保持一致）
export const FeedbackQuestionType = {
  RADIO: 'radio',
  CHECKBOX: 'checkbox',
  TEXT: 'text',
} as const

export type FeedbackQuestionTypeValue = (typeof FeedbackQuestionType)[keyof typeof FeedbackQuestionType]

export const FeedbackQuestionTypeText: Record<string, string> = {
  [FeedbackQuestionType.RADIO]: '单选题',
  [FeedbackQuestionType.CHECKBOX]: '多选题',
  [FeedbackQuestionType.TEXT]: '文本题',
}

export const FeedbackQuestionTypeOptions = [
  { label: '单选题', value: FeedbackQuestionType.RADIO },
  { label: '多选题', value: FeedbackQuestionType.CHECKBOX },
  { label: '文本题', value: FeedbackQuestionType.TEXT },
]
