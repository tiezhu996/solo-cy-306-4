import type { ActivityStatusType, ActivityTypeType } from '@/constants/activity'
import type { FeedbackQuestionTypeValue } from '@/constants/feedback'


export interface User {
  id: number
  username: string
  nickname: string
  avatar: string
  role: string
  email: string
  phone: string
  created_at: string
}

export interface Activity {
  id: number
  title: string
  description: string
  cover_image: string
  activity_type: ActivityTypeType
  start_time: string
  end_time: string
  location: string
  capacity: number
  signup_deadline: string
  status: ActivityStatusType
  organizer_id: number
  created_at: string
  registered_count?: number
}

export interface Registration {
  id: number
  activity_id: number
  user_id: number
  name: string
  phone: string
  remark: string
  voucher_no: string
  status: string
  review_status: string
  created_at: string
}

export interface CheckInRecord {
  id: number
  registration_id: number
  activity_id: number
  check_in_method: string
  check_in_time: string
  operator_id: number
}

export interface CommentItem {
  id: number
  activity_id: number
  user_id: number
  rating: number
  content: string
  created_at: string
}

export interface Favorite {
  id: number
  user_id: number
  activity_id: number
  created_at: string
}

export interface NotificationItem {
  id: number
  user_id: number
  notification_type: string
  title: string
  content: string
  is_read: boolean
  created_at: string
  type_text?: string
}

export interface PageResult<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

export interface FeedbackSurvey {
  id: number
  activity_id: number
  title: string
  description: string
  status: 'draft' | 'published'
  creator_id: number
  created_at: string
  updated_at: string
}

export interface FeedbackQuestion {
  id: number
  survey_id: number
  question_type: FeedbackQuestionTypeValue
  title: string
  options: string[]
  required: boolean
  sort_order: number
}

export interface FeedbackAnswer {
  id?: number
  response_id?: number
  question_id: number
  content: string
  values: string[]
}

export interface FeedbackOptionStat {
  option: string
  count: number
}

export interface FeedbackTextStat {
  content: string
  created_at: string
}

export interface FeedbackQuestionStat {
  question_id: number
  title: string
  question_type: FeedbackQuestionTypeValue
  answered_count: number
  options?: FeedbackOptionStat[]
  texts?: FeedbackTextStat[]
}

export interface FeedbackStats {
  survey: FeedbackSurvey
  questions: FeedbackQuestion[]
  response_count: number
  question_stats: FeedbackQuestionStat[]
}

