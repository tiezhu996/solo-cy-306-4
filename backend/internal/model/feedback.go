package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// 反馈问卷状态与题型枚举统一在 constants/feedback.go 维护，本表通过字段值与之对应。

// StringList 以 JSON 字符串形式持久化的字符串列表（问题选项、选择答案）。
type StringList []string

// Value 实现 driver.Valuer，写入数据库时序列化为 JSON。
func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner，从数据库读取 JSON 字符串。
func (l *StringList) Scan(src any) error {
	if src == nil {
		*l = StringList{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("StringList: unsupported scan source")
	}
	if len(b) == 0 {
		*l = StringList{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*l = out
	return nil
}

// MarshalJSON 保证 nil 列表也序列化为 []。
func (l StringList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(l))
}

// FeedbackSurvey 反馈问卷实体：一场已结束活动至多一份问卷。
type FeedbackSurvey struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ActivityID  uint64    `gorm:"not null;uniqueIndex" json:"activity_id"`
	Title       string    `gorm:"size:200;not null;default:''" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	Status      string    `gorm:"size:20;not null;default:draft;index" json:"status"`
	CreatorID   uint64    `gorm:"not null" json:"creator_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (FeedbackSurvey) TableName() string { return "feedback_surveys" }

// FeedbackQuestion 问卷题目实体。
type FeedbackQuestion struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	SurveyID     uint64     `gorm:"not null;index" json:"survey_id"`
	QuestionType string     `gorm:"size:20;not null;default:radio" json:"question_type"`
	Title        string     `gorm:"size:255;not null" json:"title"`
	Options      StringList `gorm:"type:json" json:"options"`
	Required     bool       `gorm:"not null;default:true" json:"required"`
	SortOrder    int        `gorm:"not null;default:0" json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
}

// TableName 指定表名。
func (FeedbackQuestion) TableName() string { return "feedback_questions" }

// FeedbackResponse 问卷提交实体：每名已签到参加者对每份问卷仅可提交一次。
type FeedbackResponse struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	SurveyID  uint64    `gorm:"not null;uniqueIndex:uk_feedback_response_survey_user" json:"survey_id"`
	UserID    uint64    `gorm:"not null;uniqueIndex:uk_feedback_response_survey_user;index" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (FeedbackResponse) TableName() string { return "feedback_responses" }

// FeedbackAnswer 单题回答实体。
type FeedbackAnswer struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ResponseID uint64     `gorm:"not null;index" json:"response_id"`
	QuestionID uint64     `gorm:"not null;index" json:"question_id"`
	Content    string     `gorm:"type:text" json:"content"`
	Values     StringList `gorm:"type:json" json:"values"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName 指定表名。
func (FeedbackAnswer) TableName() string { return "feedback_answers" }
