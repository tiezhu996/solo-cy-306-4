package constants

// 反馈问卷状态枚举。
const (
	FeedbackStatusDraft     = "draft"
	FeedbackStatusPublished = "published"
)

// FeedbackStatusValues 全部问卷状态值。
var FeedbackStatusValues = []string{FeedbackStatusDraft, FeedbackStatusPublished}

// 反馈问卷题型枚举。
const (
	FeedbackQuestionRadio    = "radio"
	FeedbackQuestionCheckbox = "checkbox"
	FeedbackQuestionText     = "text"
)

// FeedbackQuestionTypeValues 全部问卷题型值。
var FeedbackQuestionTypeValues = []string{FeedbackQuestionRadio, FeedbackQuestionCheckbox, FeedbackQuestionText}

// IsValidFeedbackStatus 校验问卷状态。
func IsValidFeedbackStatus(s string) bool {
	for _, v := range FeedbackStatusValues {
		if v == s {
			return true
		}
	}
	return false
}

// IsValidFeedbackQuestionType 校验问卷题型。
func IsValidFeedbackQuestionType(s string) bool {
	for _, v := range FeedbackQuestionTypeValues {
		if v == s {
			return true
		}
	}
	return false
}
