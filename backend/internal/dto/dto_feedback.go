package dto

// FeedbackQuestionInput 创建/编辑问卷时的单题入参。
type FeedbackQuestionInput struct {
	QuestionType string   `json:"question_type" binding:"required,oneof=radio checkbox text"`
	Title        string   `json:"title" binding:"required,max=255"`
	Options      []string `json:"options" binding:"omitempty,dive,max=100"`
	Required     *bool    `json:"required"`
}

// FeedbackSurveyCreateRequest 创建问卷请求。
type FeedbackSurveyCreateRequest struct {
	Title       string                  `json:"title" binding:"max=200"`
	Description string                  `json:"description" binding:"max=2000"`
	Questions   []FeedbackQuestionInput `json:"questions" binding:"required,min=1,max=50,dive"`
}

// FeedbackAnswerInput 提交问卷时的单题回答。
type FeedbackAnswerInput struct {
	QuestionID uint64   `json:"question_id" binding:"required"`
	Values     []string `json:"values"`
	Content    string   `json:"content" binding:"max=2000"`
}

// FeedbackSubmitRequest 提交问卷请求。
type FeedbackSubmitRequest struct {
	Answers []FeedbackAnswerInput `json:"answers" binding:"required,min=1,max=50,dive"`
}
