package service

import (
	"errors"
	"log/slog"
	"strings"

	"gbevent/internal/constants"
	"gbevent/internal/dto"
	"gbevent/internal/model"
	"gbevent/internal/repository"
	"gbevent/internal/util"

	"gorm.io/gorm"
)

// FeedbackService 反馈问卷业务逻辑。
type FeedbackService struct {
	db          *gorm.DB
	repo        *repository.FeedbackRepository
	activitySvc *ActivityService
	regRepo     *repository.RegistrationRepository
	logger      *slog.Logger
}

// NewFeedbackService 构造反馈问卷服务。
func NewFeedbackService(db *gorm.DB, repo *repository.FeedbackRepository,
	activitySvc *ActivityService, regRepo *repository.RegistrationRepository, logger *slog.Logger) *FeedbackService {
	return &FeedbackService{db: db, repo: repo, activitySvc: activitySvc, regRepo: regRepo, logger: logger}
}

// SurveyDetail 问卷详情（含题目）。
type SurveyDetail struct {
	Survey    *model.FeedbackSurvey
	Questions []model.FeedbackQuestion
}

// CreateSurvey 组织者为已结束活动创建问卷（草稿态）。
func (s *FeedbackService) CreateSurvey(activityID, operatorID uint64, operatorRole string, req dto.FeedbackSurveyCreateRequest) (*SurveyDetail, error) {
	a, _, err := s.activitySvc.Get(activityID)
	if err != nil {
		return nil, err
	}
	if !IsOrganizer(operatorID, operatorRole, a.OrganizerID) {
		return nil, util.NewAppError(constants.CodeForbidden, "FeedbackSurvey[activity_id="+itoa(activityID)+"] create forbidden: organizer not match")
	}
	if a.Status != constants.ActivityStatusEnded {
		return nil, util.NewAppError(constants.CodeConflict, constants.MsgSurveyActivityOpen)
	}
	if _, err := s.repo.FindSurveyByActivity(activityID); err == nil {
		return nil, util.NewAppError(constants.CodeSurveyExists, constants.MsgSurveyExists)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, util.Wrap(err, "FeedbackSurvey[activity_id=%d] create find failed", activityID)
	}

	questions, err := buildQuestions(req.Questions)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "《" + a.Title + "》活动反馈问卷"
	}

	survey := &model.FeedbackSurvey{
		ActivityID:  activityID,
		Title:       title,
		Description: req.Description,
		Status:      constants.FeedbackStatusDraft,
		CreatorID:   operatorID,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.repo.CreateSurveyTx(tx, survey); err != nil {
			s.logger.Error(constants.LogFeedbackSurveyCreateFailed, "error", err)
			return util.Wrap(err, "FeedbackSurvey[activity_id=%d] create failed", activityID)
		}
		for i := range questions {
			questions[i].SurveyID = survey.ID
		}
		if err := s.repo.CreateQuestionsTx(tx, questions); err != nil {
			return util.Wrap(err, "FeedbackSurvey[id=%d] create questions failed", survey.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogFeedbackSurveyCreateSuccess, "survey_id", survey.ID, "activity_id", activityID)
	return &SurveyDetail{Survey: survey, Questions: questions}, nil
}

// PublishSurvey 发布问卷（草稿 -> 已发布），发布后参加者可填写。
func (s *FeedbackService) PublishSurvey(activityID, operatorID uint64, operatorRole string) (*model.FeedbackSurvey, error) {
	survey, err := s.loadOrganizerSurvey(activityID, operatorID, operatorRole)
	if err != nil {
		return nil, err
	}
	if survey.Status != constants.FeedbackStatusDraft {
		return nil, util.NewAppError(constants.CodeConflict, "FeedbackSurvey[id="+itoa(survey.ID)+"] publish conflict: status="+survey.Status)
	}
	questions, err := s.repo.ListQuestions(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] publish list questions failed", survey.ID)
	}
	if len(questions) == 0 {
		return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackSurvey[id="+itoa(survey.ID)+"] publish: no questions")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.repo.UpdateSurveyStatusTx(tx, survey.ID, constants.FeedbackStatusPublished)
	}); err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] publish failed", survey.ID)
	}
	survey.Status = constants.FeedbackStatusPublished
	s.logger.Info(constants.LogFeedbackSurveyPublishSuccess, "survey_id", survey.ID)
	return survey, nil
}

// GetForParticipant 参加者查看问卷：返回题目、本人是否已提交及提交内容。
func (s *FeedbackService) GetForParticipant(activityID, userID uint64) (map[string]any, error) {
	survey, err := s.repo.FindSurveyByActivity(activityID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, constants.MsgSurveyNone)
		}
		return nil, util.Wrap(err, "FeedbackSurvey[activity_id=%d] get failed", activityID)
	}
	if survey.Status != constants.FeedbackStatusPublished {
		return nil, util.NewAppError(constants.CodeSurveyNotPublished, constants.MsgSurveyNotPublished)
	}
	questions, err := s.repo.ListQuestions(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] list questions failed", survey.ID)
	}
	checkedIn := s.checkCheckedIn(activityID, userID) == nil
	resp := map[string]any{
		"survey":     survey,
		"questions":  questions,
		"submitted":  false,
		"checked_in": checkedIn,
		"can_submit": checkedIn,
	}
	if existing, err := s.repo.FindResponse(survey.ID, userID); err == nil {
		mine, err := s.repo.ListAnswersByResponse(existing.ID)
		if err != nil {
			return nil, util.Wrap(err, "FeedbackSurvey[id=%d] list my answers failed", survey.ID)
		}
		resp["submitted"] = true
		resp["can_submit"] = false
		resp["submitted_at"] = existing.CreatedAt
		resp["answers"] = mine
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] find response failed", survey.ID)
	}
	return resp, nil
}

// Submit 已签到参加者提交问卷，每人仅可提交一次且不可修改。
func (s *FeedbackService) Submit(activityID, userID uint64, req dto.FeedbackSubmitRequest) (*model.FeedbackResponse, error) {
	survey, err := s.repo.FindSurveyByActivity(activityID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, constants.MsgSurveyNone)
		}
		return nil, util.Wrap(err, "FeedbackSurvey[activity_id=%d] submit find failed", activityID)
	}
	if survey.Status != constants.FeedbackStatusPublished {
		return nil, util.NewAppError(constants.CodeSurveyNotPublished, constants.MsgSurveyNotPublished)
	}
	if err := s.checkCheckedIn(activityID, userID); err != nil {
		return nil, err
	}
	questions, err := s.repo.ListQuestions(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] submit list questions failed", survey.ID)
	}
	answers, err := validateAnswers(questions, req.Answers)
	if err != nil {
		return nil, err
	}

	var resp *model.FeedbackResponse
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if existing, ferr := s.repo.FindResponseForUpdateTx(tx, survey.ID, userID); ferr == nil {
			_ = existing
			return util.NewAppError(constants.CodeSurveySubmitted, constants.MsgSurveySubmitted)
		} else if !errors.Is(ferr, repository.ErrNotFound) {
			return util.Wrap(ferr, "FeedbackSurvey[id=%d] submit find response failed", survey.ID)
		}
		resp = &model.FeedbackResponse{SurveyID: survey.ID, UserID: userID}
		if err := s.repo.CreateResponseTx(tx, resp); err != nil {
			s.logger.Error(constants.LogFeedbackSurveySubmitFailed, "error", err)
			return util.Wrap(err, "FeedbackSurvey[id=%d] submit create response failed", survey.ID)
		}
		for i := range answers {
			answers[i].ResponseID = resp.ID
		}
		if err := s.repo.CreateAnswersTx(tx, answers); err != nil {
			return util.Wrap(err, "FeedbackResponse[id=%d] create answers failed", resp.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogFeedbackSurveySubmitSuccess, "survey_id", survey.ID, "response_id", resp.ID, "user_id", userID)
	return resp, nil
}

// Stats 组织者查看问卷统计：填写人数、每题选项计数与文本回答。
func (s *FeedbackService) Stats(activityID, operatorID uint64, operatorRole string) (map[string]any, error) {
	survey, err := s.loadOrganizerSurvey(activityID, operatorID, operatorRole)
	if err != nil {
		return nil, err
	}
	questions, err := s.repo.ListQuestions(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] stats list questions failed", survey.ID)
	}
	count, err := s.repo.CountResponses(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] stats count failed", survey.ID)
	}
	answers, err := s.repo.ListAnswers(survey.ID)
	if err != nil {
		return nil, util.Wrap(err, "FeedbackSurvey[id=%d] stats list answers failed", survey.ID)
	}

	type optionStat struct {
		Option string `json:"option"`
		Count  int    `json:"count"`
	}
	type textAnswer struct {
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}
	type questionStat struct {
		QuestionID uint64       `json:"question_id"`
		Title      string       `json:"title"`
		Type       string       `json:"question_type"`
		Answered   int          `json:"answered_count"`
		Options    []optionStat `json:"options,omitempty"`
		Texts      []textAnswer `json:"texts,omitempty"`
	}

	byQuestion := make(map[uint64][]model.FeedbackAnswer, len(questions))
	for _, a := range answers {
		byQuestion[a.QuestionID] = append(byQuestion[a.QuestionID], a)
	}
	stats := make([]questionStat, 0, len(questions))
	for _, q := range questions {
		qs := questionStat{QuestionID: q.ID, Title: q.Title, Type: q.QuestionType}
		list := byQuestion[q.ID]
		if q.QuestionType == constants.FeedbackQuestionText {
			qs.Texts = make([]textAnswer, 0, len(list))
			for _, a := range list {
				if strings.TrimSpace(a.Content) == "" {
					continue
				}
				qs.Texts = append(qs.Texts, textAnswer{Content: a.Content, CreatedAt: a.CreatedAt.Format("2006-01-02 15:04:05")})
			}
			qs.Answered = len(qs.Texts)
		} else {
			counts := make(map[string]int)
			answered := 0
			for _, a := range list {
				if len(a.Values) == 0 {
					continue
				}
				answered++
				for _, v := range a.Values {
					counts[v]++
				}
			}
			qs.Options = make([]optionStat, 0, len(q.Options))
			for _, opt := range q.Options {
				qs.Options = append(qs.Options, optionStat{Option: opt, Count: counts[opt]})
			}
			qs.Answered = answered
		}
		stats = append(stats, qs)
	}
	return map[string]any{
		"survey":         survey,
		"questions":      questions,
		"response_count": count,
		"question_stats": stats,
	}, nil
}

// loadOrganizerSurvey 按活动加载问卷并校验组织者权限。
func (s *FeedbackService) loadOrganizerSurvey(activityID, operatorID uint64, operatorRole string) (*model.FeedbackSurvey, error) {
	a, _, err := s.activitySvc.Get(activityID)
	if err != nil {
		return nil, err
	}
	if !IsOrganizer(operatorID, operatorRole, a.OrganizerID) {
		return nil, util.NewAppError(constants.CodeForbidden, "FeedbackSurvey[activity_id="+itoa(activityID)+"] forbidden: organizer not match")
	}
	survey, err := s.repo.FindSurveyByActivity(activityID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, constants.MsgSurveyNone)
		}
		return nil, util.Wrap(err, "FeedbackSurvey[activity_id=%d] find failed", activityID)
	}
	return survey, nil
}

// checkCheckedIn 校验用户在该活动已签到。
func (s *FeedbackService) checkCheckedIn(activityID, userID uint64) error {
	reg, err := s.regRepo.FindByActivityAndUser(activityID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeNotCheckedIn, constants.MsgNotCheckedIn)
		}
		return util.Wrap(err, "FeedbackSurvey[activity_id=%d] check registration failed", activityID)
	}
	if reg.Status != constants.RegistrationStatusCheckedIn {
		return util.NewAppError(constants.CodeNotCheckedIn, constants.MsgNotCheckedIn)
	}
	return nil
}

// buildQuestions 校验入参题目并构造实体（选择题至少两个不重复选项）。
func buildQuestions(input []dto.FeedbackQuestionInput) ([]model.FeedbackQuestion, error) {
	questions := make([]model.FeedbackQuestion, 0, len(input))
	for i, in := range input {
		q := model.FeedbackQuestion{
			QuestionType: in.QuestionType,
			Title:        strings.TrimSpace(in.Title),
			Required:     true,
			SortOrder:    i,
		}
		if in.Required != nil {
			q.Required = *in.Required
		}
		if q.Title == "" {
			return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[index="+itoa(uint64(i))+"] title is empty")
		}
		switch in.QuestionType {
		case constants.FeedbackQuestionRadio, constants.FeedbackQuestionCheckbox:
			seen := make(map[string]struct{})
			for _, opt := range in.Options {
				opt = strings.TrimSpace(opt)
				if opt == "" {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[index="+itoa(uint64(i))+"] option is empty")
				}
				if _, dup := seen[opt]; dup {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[index="+itoa(uint64(i))+"] duplicate option: "+opt)
				}
				seen[opt] = struct{}{}
				q.Options = append(q.Options, opt)
			}
			if len(q.Options) < 2 {
				return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[index="+itoa(uint64(i))+"] choice question needs at least 2 options")
			}
		case constants.FeedbackQuestionText:
			q.Options = model.StringList{}
		}
		questions = append(questions, q)
	}
	return questions, nil
}

// validateAnswers 按题目定义校验提交答案，返回待持久化的回答实体。
func validateAnswers(questions []model.FeedbackQuestion, input []dto.FeedbackAnswerInput) ([]model.FeedbackAnswer, error) {
	byID := make(map[uint64]model.FeedbackQuestion, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	seen := make(map[uint64]struct{}, len(input))
	answers := make([]model.FeedbackAnswer, 0, len(input))
	for _, in := range input {
		q, ok := byID[in.QuestionID]
		if !ok {
			return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackAnswer[question_id="+itoa(in.QuestionID)+"] question not found")
		}
		if _, dup := seen[in.QuestionID]; dup {
			return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackAnswer[question_id="+itoa(in.QuestionID)+"] duplicated answer")
		}
		seen[in.QuestionID] = struct{}{}

		a := model.FeedbackAnswer{QuestionID: q.ID}
		content := strings.TrimSpace(in.Content)
		switch q.QuestionType {
		case constants.FeedbackQuestionRadio:
			if len(in.Values) > 1 {
				return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] radio accepts only one option")
			}
			if len(in.Values) == 1 {
				if !optionAllowed(q.Options, in.Values[0]) {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] invalid option: "+in.Values[0])
				}
				a.Values = model.StringList{in.Values[0]}
			}
		case constants.FeedbackQuestionCheckbox:
			seenOpt := make(map[string]struct{})
			for _, v := range in.Values {
				if !optionAllowed(q.Options, v) {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] invalid option: "+v)
				}
				if _, dup := seenOpt[v]; dup {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] duplicated option: "+v)
				}
				seenOpt[v] = struct{}{}
				a.Values = append(a.Values, v)
			}
		case constants.FeedbackQuestionText:
			a.Content = content
		}
		if q.Required {
			if q.QuestionType == constants.FeedbackQuestionText {
				if content == "" {
					return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] answer required")
				}
			} else if len(a.Values) == 0 {
				return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] answer required")
			}
		}
		answers = append(answers, a)
	}
	for _, q := range questions {
		if _, ok := seen[q.ID]; !ok && q.Required {
			return nil, util.NewAppError(constants.CodeValidationFailed, "FeedbackQuestion[id="+itoa(q.ID)+"] answer required")
		}
	}
	return answers, nil
}

func optionAllowed(options model.StringList, v string) bool {
	for _, opt := range options {
		if opt == v {
			return true
		}
	}
	return false
}
