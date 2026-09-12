package repository

import (
	"errors"
	"fmt"
	"strings"

	"gbevent/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// isDuplicateKeyErr 识别唯一索引冲突（MySQL 1062 / SQLite UNIQUE / GORM 翻译错误）。
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "unique constraint failed")
}

// FeedbackRepository 反馈问卷仓储（问卷/题目/提交/回答同库管理）。
type FeedbackRepository struct {
	db *gorm.DB
}

// NewFeedbackRepository 构造反馈问卷仓储。
func NewFeedbackRepository(db *gorm.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// ---- 问卷 ----

// FindSurveyByActivity 按活动 ID 查询问卷。
func (r *FeedbackRepository) FindSurveyByActivity(activityID uint64) (*model.FeedbackSurvey, error) {
	var s model.FeedbackSurvey
	if err := r.db.Where("activity_id = ?", activityID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find feedback survey by activity: %w", err)
	}
	return &s, nil
}

// FindSurveyByID 按问卷 ID 查询问卷。
func (r *FeedbackRepository) FindSurveyByID(id uint64) (*model.FeedbackSurvey, error) {
	var s model.FeedbackSurvey
	if err := r.db.First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find feedback survey by id: %w", err)
	}
	return &s, nil
}

// CreateSurveyTx 事务内创建问卷。
func (r *FeedbackRepository) CreateSurveyTx(tx *gorm.DB, s *model.FeedbackSurvey) error {
	if err := tx.Create(s).Error; err != nil {
		return fmt.Errorf("create feedback survey: %w", err)
	}
	return nil
}

// UpdateSurveyStatusTx 事务内更新问卷状态。
func (r *FeedbackRepository) UpdateSurveyStatusTx(tx *gorm.DB, id uint64, status string) error {
	if err := tx.Model(&model.FeedbackSurvey{}).Where("id = ?", id).Update("status", status).Error; err != nil {
		return fmt.Errorf("update feedback survey status: %w", err)
	}
	return nil
}

// ---- 题目 ----

// CreateQuestionsTx 事务内批量创建题目。
func (r *FeedbackRepository) CreateQuestionsTx(tx *gorm.DB, questions []model.FeedbackQuestion) error {
	if len(questions) == 0 {
		return nil
	}
	if err := tx.Create(&questions).Error; err != nil {
		return fmt.Errorf("create feedback questions: %w", err)
	}
	return nil
}

// ListQuestions 查询问卷的全部题目（按排序与 ID 升序）。
func (r *FeedbackRepository) ListQuestions(surveyID uint64) ([]model.FeedbackQuestion, error) {
	var list []model.FeedbackQuestion
	if err := r.db.Where("survey_id = ?", surveyID).Order("sort_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list feedback questions: %w", err)
	}
	return list, nil
}

// ---- 提交 ----

// FindResponse 查询某用户对某问卷的提交。
func (r *FeedbackRepository) FindResponse(surveyID, userID uint64) (*model.FeedbackResponse, error) {
	var resp model.FeedbackResponse
	if err := r.db.Where("survey_id = ? AND user_id = ?", surveyID, userID).First(&resp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find feedback response: %w", err)
	}
	return &resp, nil
}

// FindResponseForUpdateTx 事务内锁定提交行，防止并发重复提交。
func (r *FeedbackRepository) FindResponseForUpdateTx(tx *gorm.DB, surveyID, userID uint64) (*model.FeedbackResponse, error) {
	var resp model.FeedbackResponse
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("survey_id = ? AND user_id = ?", surveyID, userID).First(&resp).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find feedback response for update: %w", err)
	}
	return &resp, nil
}

// CreateResponseTx 事务内创建提交。
func (r *FeedbackRepository) CreateResponseTx(tx *gorm.DB, resp *model.FeedbackResponse) error {
	if err := tx.Create(resp).Error; err != nil {
		if isDuplicateKeyErr(err) {
			return fmt.Errorf("create feedback response: %w: %v", ErrDuplicate, err)
		}
		return fmt.Errorf("create feedback response: %w", err)
	}
	return nil
}

// CountResponses 统计问卷提交人数。
func (r *FeedbackRepository) CountResponses(surveyID uint64) (int64, error) {
	var n int64
	if err := r.db.Model(&model.FeedbackResponse{}).Where("survey_id = ?", surveyID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count feedback responses: %w", err)
	}
	return n, nil
}

// ---- 回答 ----

// CreateAnswersTx 事务内批量创建回答。
func (r *FeedbackRepository) CreateAnswersTx(tx *gorm.DB, answers []model.FeedbackAnswer) error {
	if len(answers) == 0 {
		return nil
	}
	if err := tx.Create(&answers).Error; err != nil {
		return fmt.Errorf("create feedback answers: %w", err)
	}
	return nil
}

// ListAnswersByResponse 查询某一次提交下的全部回答。
func (r *FeedbackRepository) ListAnswersByResponse(responseID uint64) ([]model.FeedbackAnswer, error) {
	var list []model.FeedbackAnswer
	if err := r.db.Where("response_id = ?", responseID).Order("question_id ASC, id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list feedback answers by response: %w", err)
	}
	return list, nil
}

// ListAnswers 查询问卷的全部回答（含每题文本/选项答案）。
func (r *FeedbackRepository) ListAnswers(surveyID uint64) ([]model.FeedbackAnswer, error) {
	var list []model.FeedbackAnswer
	if err := r.db.Table("feedback_answers AS a").
		Joins("JOIN feedback_responses AS r ON r.id = a.response_id").
		Where("r.survey_id = ?", surveyID).
		Order("a.question_id ASC, r.created_at ASC, a.id ASC").
		Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list feedback answers: %w", err)
	}
	return list, nil
}
