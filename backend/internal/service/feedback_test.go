package service

// 反馈问卷自动化测试（纯 Go sqlite，无需 MySQL/Docker）。
//
// 运行方式：
//
//	cd backend && CGO_ENABLED=0 go test ./internal/service/ -run 'TestFeedback' -v
//	cd backend && CGO_ENABLED=0 go test ./internal/service/ -run 'TestFeedbackConcurrentSubmit' -race -count=5
//
// 每个用例使用 t.TempDir() 中的独立数据库文件，重复运行互不影响。
import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gbevent/internal/constants"
	"gbevent/internal/dto"
	"gbevent/internal/model"
	"gbevent/internal/repository"
	"gbevent/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 测试夹具中的固定 ID。
const (
	fxOrganizerID  uint64 = 1 // 活动 1 的组织者
	fxOtherOrgID   uint64 = 2 // 其他组织者
	fxCheckedID    uint64 = 3 // 活动 1 已签到参加者
	fxRegisteredID uint64 = 4 // 活动 1 已报名未签到参加者
	fxNoRegID      uint64 = 5 // 未报名用户

	fxEndedActivityID      uint64 = 1
	fxPublishedActivityID  uint64 = 2
	fxOtherEndedActivityID uint64 = 3
)

// feedbackHarness 聚合问卷测试所需的服务。
type feedbackHarness struct {
	db  *gorm.DB
	svc *FeedbackService
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// openFeedbackTestDB 打开独立 sqlite 数据库：MaxOpenConns(1) 使 sqlite 写事务串行，
// 既符合并发测试预期，也能真正触发唯一索引兜底。
func openFeedbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return openFeedbackDBAt(t, filepath.Join(t.TempDir(), "test.db"))
}

// openFeedbackDBAt 打开指定路径的 sqlite 数据库（用于跨连接持久化测试）。
func openFeedbackDBAt(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.User{}, &model.Activity{}, &model.Registration{}, &model.CheckInRecord{},
		&model.Comment{}, &model.Favorite{}, &model.Notification{}, &model.AuditLog{},
		&model.FeedbackSurvey{}, &model.FeedbackQuestion{}, &model.FeedbackResponse{}, &model.FeedbackAnswer{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedFeedbackData 写入用户、活动与报名夹具。
func seedFeedbackData(t *testing.T, db *gorm.DB) {
	t.Helper()
	users := []model.User{
		{ID: fxOrganizerID, Username: "org", Role: constants.RoleOrganizer},
		{ID: fxOtherOrgID, Username: "other-org", Role: constants.RoleOrganizer},
		{ID: fxCheckedID, Username: "checked", Role: constants.RoleUser},
		{ID: fxRegisteredID, Username: "registered", Role: constants.RoleUser},
		{ID: fxNoRegID, Username: "noreg", Role: constants.RoleUser},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	now := time.Now()
	activities := []model.Activity{
		{ID: fxEndedActivityID, Title: "已结束活动", ActivityType: "lecture", StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour), SignupDeadline: now.Add(-3 * time.Hour), Status: constants.ActivityStatusEnded, OrganizerID: fxOrganizerID, Capacity: 100},
		{ID: fxPublishedActivityID, Title: "进行中活动", ActivityType: "lecture", StartTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour), SignupDeadline: now.Add(time.Hour), Status: constants.ActivityStatusPublished, OrganizerID: fxOrganizerID, Capacity: 100},
		{ID: fxOtherEndedActivityID, Title: "他人的已结束活动", ActivityType: "lecture", StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour), SignupDeadline: now.Add(-3 * time.Hour), Status: constants.ActivityStatusEnded, OrganizerID: fxOtherOrgID, Capacity: 100},
	}
	for i := range activities {
		if err := db.Create(&activities[i]).Error; err != nil {
			t.Fatalf("seed activity: %v", err)
		}
	}
	regs := []model.Registration{
		{ActivityID: fxEndedActivityID, UserID: fxCheckedID, Name: "已签到", Phone: "13900000001", VoucherNo: "V-CHECKED", Status: constants.RegistrationStatusCheckedIn, ReviewStatus: constants.ReviewStatusApproved},
		{ActivityID: fxEndedActivityID, UserID: fxRegisteredID, Name: "未签到", Phone: "13900000002", VoucherNo: "V-REGISTERED", Status: constants.RegistrationStatusRegistered, ReviewStatus: constants.ReviewStatusApproved},
	}
	for i := range regs {
		if err := db.Create(&regs[i]).Error; err != nil {
			t.Fatalf("seed registration: %v", err)
		}
	}
}

func newFeedbackHarness(t *testing.T) *feedbackHarness {
	t.Helper()
	db := openFeedbackTestDB(t)
	seedFeedbackData(t, db)
	logger := discardLogger()
	actRepo := repository.NewActivityRepository(db)
	regRepo := repository.NewRegistrationRepository(db)
	notifyRepo := repository.NewNotificationRepository(db)
	checkinRepo := repository.NewCheckInRecordRepository(db)
	fbRepo := repository.NewFeedbackRepository(db)
	actSvc := NewActivityService(actRepo, regRepo, notifyRepo, checkinRepo, logger)
	return &feedbackHarness{db: db, svc: NewFeedbackService(db, fbRepo, actSvc, regRepo, logger)}
}

// validSurveyRequest 构造合法的三题问卷（单选/多选/文本）。
func validSurveyRequest() dto.FeedbackSurveyCreateRequest {
	required := true
	optional := false
	return dto.FeedbackSurveyCreateRequest{
		Title:       "反馈问卷",
		Description: "请如实填写",
		Questions: []dto.FeedbackQuestionInput{
			{QuestionType: constants.FeedbackQuestionRadio, Title: "整体满意度", Options: []string{"满意", "一般", "不满意"}, Required: &required},
			{QuestionType: constants.FeedbackQuestionCheckbox, Title: "喜欢的环节", Options: []string{"分享", "讨论", "茶歇"}, Required: &optional},
			{QuestionType: constants.FeedbackQuestionText, Title: "意见建议", Required: &optional},
		},
	}
}

// createPublishedSurvey 创建并发布问卷，返回问卷题目 ID（按 sort_order）。
func createPublishedSurvey(t *testing.T, h *feedbackHarness, activityID uint64) *SurveyDetail {
	t.Helper()
	detail, err := h.svc.CreateSurvey(activityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest())
	if err != nil {
		t.Fatalf("create survey: %v", err)
	}
	if _, err := h.svc.PublishSurvey(activityID, fxOrganizerID, constants.RoleOrganizer); err != nil {
		t.Fatalf("publish survey: %v", err)
	}
	return detail
}

// validAnswers 根据题目顺序构造合法答案。
func validAnswers(detail *SurveyDetail) dto.FeedbackSubmitRequest {
	return dto.FeedbackSubmitRequest{Answers: []dto.FeedbackAnswerInput{
		{QuestionID: detail.Questions[0].ID, Values: []string{"满意"}},
		{QuestionID: detail.Questions[1].ID, Values: []string{"分享", "茶歇"}},
		{QuestionID: detail.Questions[2].ID, Content: "组织得很好"},
	}}
}

func appCode(t *testing.T, err error) int {
	t.Helper()
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	if err == nil {
		return constants.CodeOK
	}
	t.Fatalf("expected *util.AppError, got %T: %v", err, err)
	return 0
}

// appCodeQuiet 与 appCode 相同，但不中止测试（供并发 goroutine 使用）。
func appCodeQuiet(err error) int {
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return -1
}

func findStat(stats []struct {
	QuestionID    uint64 `json:"question_id"`
	AnsweredCount int    `json:"answered_count"`
	Options       []struct {
		Option string `json:"option"`
		Count  int    `json:"count"`
	} `json:"options"`
	Texts []struct {
		Content string `json:"content"`
	} `json:"texts"`
}, id uint64) struct {
	QuestionID    uint64 `json:"question_id"`
	AnsweredCount int    `json:"answered_count"`
	Options       []struct {
		Option string `json:"option"`
		Count  int    `json:"count"`
	} `json:"options"`
	Texts []struct {
		Content string `json:"content"`
	} `json:"texts"`
} {
	for _, s := range stats {
		if s.QuestionID == id {
			return s
		}
	}
	return struct {
		QuestionID    uint64 `json:"question_id"`
		AnsweredCount int    `json:"answered_count"`
		Options       []struct {
			Option string `json:"option"`
			Count  int    `json:"count"`
		} `json:"options"`
		Texts []struct {
			Content string `json:"content"`
		} `json:"texts"`
	}{}
}

// ---- 用例 1：组织者只能为已结束活动创建问卷 ----

func TestFeedbackCreateOnlyEndedActivity(t *testing.T) {
	// 步骤：分别对“进行中活动 2”“已结束活动 1”调用 CreateSurvey。
	// 预期：进行中 -> CodeConflict(40900) 且提示活动尚未结束；已结束 -> 成功，问卷为 draft。
	h := newFeedbackHarness(t)

	if _, err := h.svc.CreateSurvey(fxPublishedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest()); appCode(t, err) != constants.CodeConflict {
		t.Fatalf("进行中活动创建问卷应返回 CodeConflict, got code=%d err=%v", appCode(t, err), err)
	}
	detail, err := h.svc.CreateSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest())
	if err != nil {
		t.Fatalf("已结束活动创建问卷失败: %v", err)
	}
	if detail.Survey.Status != constants.FeedbackStatusDraft {
		t.Fatalf("新建问卷应为 draft, got %s", detail.Survey.Status)
	}
}

// 非组织者不能为他人活动创建；管理员可以。
func TestFeedbackCreatePermission(t *testing.T) {
	h := newFeedbackHarness(t)

	// 普通用户创建 -> 403
	if _, err := h.svc.CreateSurvey(fxEndedActivityID, fxCheckedID, constants.RoleUser, validSurveyRequest()); appCode(t, err) != constants.CodeForbidden {
		t.Fatalf("普通用户创建问卷应 403, got %d", appCode(t, err))
	}
	// 非本人组织者创建 -> 403
	if _, err := h.svc.CreateSurvey(fxOtherEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest()); appCode(t, err) != constants.CodeForbidden {
		t.Fatalf("非活动组织者创建问卷应 403, got %d", appCode(t, err))
	}
	// 管理员可替任意组织者创建
	if _, err := h.svc.CreateSurvey(fxOtherEndedActivityID, 1, constants.RoleAdmin, validSurveyRequest()); err != nil {
		t.Fatalf("管理员应可创建他人活动问卷: %v", err)
	}
}

// 同一活动重复创建 -> CodeSurveyExists。
func TestFeedbackCreateDuplicate(t *testing.T) {
	h := newFeedbackHarness(t)
	if _, err := h.svc.CreateSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest()); err != nil {
		t.Fatalf("首次创建: %v", err)
	}
	_, err := h.svc.CreateSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest())
	if appCode(t, err) != constants.CodeSurveyExists {
		t.Fatalf("重复创建应返回 CodeSurveyExists(%d), got code=%d err=%v", constants.CodeSurveyExists, appCode(t, err), err)
	}
}

// ---- 用例 2：发布规则 ----

func TestFeedbackPublishRules(t *testing.T) {
	// 步骤 A：发布“进行中活动 2”的问卷——创建阶段就应被拦截，数据库中不存在问卷。
	// 步骤 B：草稿问卷可发布一次，重复发布 -> CodeConflict；发布后参加者才可见。
	h := newFeedbackHarness(t)

	// 草稿对参加者不可见
	detail, err := h.svc.CreateSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.svc.GetForParticipant(fxEndedActivityID, fxCheckedID); appCode(t, err) != constants.CodeSurveyNotPublished {
		t.Fatalf("草稿问卷参加者应不可见 CodeSurveyNotPublished, got %d", appCode(t, err))
	}
	if _, err := h.svc.PublishSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer); err != nil {
		t.Fatalf("首次发布失败: %v", err)
	}
	if detail.Survey.Status == constants.FeedbackStatusPublished {
		t.Fatalf("Create 返回对象不应被发布操作原地修改")
	}
	if _, err := h.svc.PublishSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer); appCode(t, err) != constants.CodeConflict {
		t.Fatalf("重复发布应 CodeConflict, got %d", appCode(t, err))
	}
	// 发布后参加者可见
	got, err := h.svc.GetForParticipant(fxEndedActivityID, fxCheckedID)
	if err != nil {
		t.Fatalf("发布后参加者应可见: %v", err)
	}
	if got["submitted"] != false || got["checked_in"] != true {
		t.Fatalf("发布后初始状态错误: submitted=%v checked_in=%v", got["submitted"], got["checked_in"])
	}
}

// ---- 用例 3：未签到（含未报名）参加者不能提交 ----

func TestFeedbackSubmitRequiresCheckedIn(t *testing.T) {
	h := newFeedbackHarness(t)
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	req := validAnswers(detail)

	// 已报名未签到 -> 403 CodeNotCheckedIn
	if _, err := h.svc.Submit(fxEndedActivityID, fxRegisteredID, req); appCode(t, err) != constants.CodeNotCheckedIn {
		t.Fatalf("未签到参加者提交应 CodeNotCheckedIn(%d), got %d err=%v", constants.CodeNotCheckedIn, appCode(t, err), err)
	}
	// 未报名 -> 同样 CodeNotCheckedIn，避免枚举报名状态
	if _, err := h.svc.Submit(fxEndedActivityID, fxNoRegID, req); appCode(t, err) != constants.CodeNotCheckedIn {
		t.Fatalf("未报名用户提交应 CodeNotCheckedIn, got %d", appCode(t, err))
	}
	// 已签到用户可提交
	if _, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, req); err != nil {
		t.Fatalf("已签到参加者应能提交: %v", err)
	}
}

// 草稿问卷不能提交。
func TestFeedbackSubmitDraftRejected(t *testing.T) {
	h := newFeedbackHarness(t)
	detail, err := h.svc.CreateSurvey(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer, validSurveyRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, validAnswers(detail)); appCode(t, err) != constants.CodeSurveyNotPublished {
		t.Fatalf("草稿问卷提交应 CodeSurveyNotPublished, got %d err=%v", appCode(t, err), err)
	}
}

// ---- 用例 4：每人只能提交一次，提交后不可修改 ----

func TestFeedbackSubmitOnce(t *testing.T) {
	h := newFeedbackHarness(t)
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	req := validAnswers(detail)

	if _, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, req); err != nil {
		t.Fatalf("首次提交失败: %v", err)
	}
	// 再次提交（相同或修改后的答案）-> CodeSurveySubmitted
	_, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, req)
	if appCode(t, err) != constants.CodeSurveySubmitted {
		t.Fatalf("重复提交应 CodeSurveySubmitted(%d), got code=%d err=%v", constants.CodeSurveySubmitted, appCode(t, err), err)
	}
	// 已提交记录数仍为 1，回答仍为首次内容
	var respCount int64
	if err := h.db.Model(&model.FeedbackResponse{}).Where("survey_id = ?", detail.Survey.ID).Count(&respCount).Error; err != nil {
		t.Fatalf("count responses: %v", err)
	}
	if respCount != 1 {
		t.Fatalf("重复提交后记录数应为 1, got %d", respCount)
	}
	got, err := h.svc.GetForParticipant(fxEndedActivityID, fxCheckedID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["submitted"] != true {
		t.Fatalf("提交后 submitted 应为 true")
	}
	answers := got["answers"].([]model.FeedbackAnswer)
	if len(answers) != 3 {
		t.Fatalf("应回显 3 条回答, got %d", len(answers))
	}
	var text string
	for _, a := range answers {
		if a.QuestionID == detail.Questions[2].ID {
			text = a.Content
		}
	}
	if text != "组织得很好" {
		t.Fatalf("首次文本答案被篡改: %q", text)
	}
}

// ---- 用例 5：单选/多选答案校验 ----

func TestFeedbackAnswerValidation(t *testing.T) {
	h := newFeedbackHarness(t)
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	radioID := detail.Questions[0].ID
	checkID := detail.Questions[1].ID
	textID := detail.Questions[2].ID

	cases := []struct {
		name    string
		answers []dto.FeedbackAnswerInput
	}{
		{
			"必答单选题缺失",
			[]dto.FeedbackAnswerInput{{QuestionID: checkID, Values: []string{"分享"}}},
		},
		{
			"单选题选择多个选项",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"满意", "一般"}}, {QuestionID: textID, Content: "x"}},
		},
		{
			"单选题选择不存在的选项",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"不存在"}}, {QuestionID: textID, Content: "x"}},
		},
		{
			"多选题包含非法选项",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"满意"}}, {QuestionID: checkID, Values: []string{"分享", "非法"}}},
		},
		{
			"多选题重复选项",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"满意"}}, {QuestionID: checkID, Values: []string{"分享", "分享"}}},
		},
		{
			"回答了不属于问卷的题目",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"满意"}}, {QuestionID: 99999, Content: "x"}},
		},
		{
			"同一题回答两次",
			[]dto.FeedbackAnswerInput{{QuestionID: radioID, Values: []string{"满意"}}, {QuestionID: radioID, Values: []string{"一般"}}, {QuestionID: textID, Content: "x"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个子用例用全新的已发布问卷，避免互相污染提交状态。
			h2 := newFeedbackHarness(t)
			d2 := createPublishedSurvey(t, h2, fxEndedActivityID)
			// 用新问卷的题目 ID 替换夹具中的题目引用（除“非法题目 ID”用例外）
			ans := remapAnswers(tc.answers, detail.Questions, d2.Questions)
			_, err := h2.svc.Submit(fxEndedActivityID, fxCheckedID, dto.FeedbackSubmitRequest{Answers: ans})
			if appCode(t, err) != constants.CodeValidationFailed {
				t.Fatalf("%q 应返回 CodeValidationFailed(%d), got code=%d err=%v", tc.name, constants.CodeValidationFailed, appCode(t, err), err)
			}
			// 校验失败不应产生任何提交记录
			var n int64
			if err := h2.db.Model(&model.FeedbackResponse{}).Count(&n).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if n != 0 {
				t.Fatalf("%q 校验失败不应写入提交记录, got %d", tc.name, n)
			}
		})
	}
}

// remapAnswers 将基于旧题目 ID 的答案映射到新问卷的同序题目 ID。
func remapAnswers(in []dto.FeedbackAnswerInput, oldQs, newQs []model.FeedbackQuestion) []dto.FeedbackAnswerInput {
	idMap := make(map[uint64]uint64, len(oldQs))
	for i := range oldQs {
		idMap[oldQs[i].ID] = newQs[i].ID
	}
	out := make([]dto.FeedbackAnswerInput, len(in))
	for i, a := range in {
		out[i] = a
		if mapped, ok := idMap[a.QuestionID]; ok {
			out[i].QuestionID = mapped
		}
	}
	return out
}

// 非必答文本题可留空。
func TestFeedbackOptionalQuestionCanBeEmpty(t *testing.T) {
	h := newFeedbackHarness(t)
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	req := dto.FeedbackSubmitRequest{Answers: []dto.FeedbackAnswerInput{
		{QuestionID: detail.Questions[0].ID, Values: []string{"一般"}},
		// 多选与文本均为非必答，直接缺省
	}}
	if _, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, req); err != nil {
		t.Fatalf("非必答缺省应允许提交: %v", err)
	}
}

// ---- 用例 6：文本回答统计（含选项计数与填写人数）----

func TestFeedbackStatsAggregation(t *testing.T) {
	// 步骤：构造 3 名已签到参加者并提交不同答案，校验 Stats 聚合。
	h := newFeedbackHarness(t)
	// 额外两名已签到用户
	for _, uid := range []uint64{fxRegisteredID, fxNoRegID} {
		reg := &model.Registration{ActivityID: fxEndedActivityID, UserID: uid, Name: "p", Phone: "p", VoucherNo: "V-" + string(rune('A'+uid)), Status: constants.RegistrationStatusCheckedIn, ReviewStatus: constants.ReviewStatusApproved}
		// fxRegisteredID 已存在 registered 报名，改为直接更新状态；fxNoRegID 新建
		if uid == fxRegisteredID {
			if err := h.db.Model(&model.Registration{}).Where("activity_id = ? AND user_id = ?", fxEndedActivityID, uid).
				Update("status", constants.RegistrationStatusCheckedIn).Error; err != nil {
				t.Fatalf("upgrade checkin: %v", err)
			}
			continue
		}
		if err := h.db.Create(reg).Error; err != nil {
			t.Fatalf("seed checked user: %v", err)
		}
	}
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	radioID, checkID, textID := detail.Questions[0].ID, detail.Questions[1].ID, detail.Questions[2].ID

	submit := func(uid uint64, radio string, checks []string, text string) {
		t.Helper()
		_, err := h.svc.Submit(fxEndedActivityID, uid, dto.FeedbackSubmitRequest{Answers: []dto.FeedbackAnswerInput{
			{QuestionID: radioID, Values: []string{radio}},
			{QuestionID: checkID, Values: checks},
			{QuestionID: textID, Content: text},
		}})
		if err != nil {
			t.Fatalf("user %d submit: %v", uid, err)
		}
	}
	submit(fxCheckedID, "满意", []string{"分享", "茶歇"}, "很好")
	submit(fxRegisteredID, "满意", []string{"分享"}, "")
	submit(fxNoRegID, "一般", nil, "希望增加互动")

	stats, err := h.svc.Stats(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if got := stats["response_count"].(int64); got != 3 {
		t.Fatalf("填写人数应为 3, got %d", got)
	}
	qStats := stats["question_stats"]
	raw, err := json.Marshal(qStats)
	if err != nil {
		t.Fatalf("marshal stats: %v", err)
	}
	var parsed []struct {
		QuestionID    uint64 `json:"question_id"`
		AnsweredCount int    `json:"answered_count"`
		Options       []struct {
			Option string `json:"option"`
			Count  int    `json:"count"`
		} `json:"options"`
		Texts []struct {
			Content string `json:"content"`
		} `json:"texts"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if len(parsed) != 3 {
		t.Fatalf("应有 3 题统计, got %d", len(parsed))
	}
	radioStat := findStat(parsed, radioID)
	counts := map[string]int{}
	for _, o := range radioStat.Options {
		counts[o.Option] = o.Count
	}
	if counts["满意"] != 2 || counts["一般"] != 1 || counts["不满意"] != 0 {
		t.Fatalf("单选计数错误: %+v", counts)
	}
	checkStat := findStat(parsed, checkID)
	checkCounts := map[string]int{}
	for _, o := range checkStat.Options {
		checkCounts[o.Option] = o.Count
	}
	if checkCounts["分享"] != 2 || checkCounts["茶歇"] != 1 || checkCounts["讨论"] != 0 {
		t.Fatalf("多选计数错误: %+v", checkCounts)
	}
	if checkStat.AnsweredCount != 2 {
		t.Fatalf("多选题已答应为 2 人, got %d", checkStat.AnsweredCount)
	}
	textStat := findStat(parsed, textID)
	if textStat.AnsweredCount != 2 {
		t.Fatalf("文本题已答应为 2（空文本不计）, got %d", textStat.AnsweredCount)
	}
	gotTexts := map[string]bool{}
	for _, tx := range textStat.Texts {
		gotTexts[tx.Content] = true
	}
	if !gotTexts["很好"] || !gotTexts["希望增加互动"] {
		t.Fatalf("文本回答统计缺失: %+v", gotTexts)
	}
}

// ---- 用例 7：并发提交不产生重复记录 ----

func TestFeedbackConcurrentSubmit(t *testing.T) {
	// 步骤：同一已签到用户对同一问卷并发提交 16 次。
	// 预期：恰好 1 次成功，其余全部 CodeSurveySubmitted；feedback_responses 仅 1 行。
	h := newFeedbackHarness(t)
	detail := createPublishedSurvey(t, h, fxEndedActivityID)
	req := validAnswers(detail)

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, dup, other := 0, 0, 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := h.svc.Submit(fxEndedActivityID, fxCheckedID, req)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case appCodeQuiet(err) == constants.CodeSurveySubmitted:
				dup++
			default:
				other++
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("并发提交应恰好 1 次成功, got %d (dup=%d other=%d)", success, dup, other)
	}
	if dup != n-1 {
		t.Fatalf("其余 %d 次应返回重复提交, got dup=%d other=%d", n-1, dup, other)
	}
	var count int64
	if err := h.db.Model(&model.FeedbackResponse{}).Where("survey_id = ? AND user_id = ?", detail.Survey.ID, fxCheckedID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("数据库中提交记录必须为 1 行, got %d", count)
	}
	var answerCount int64
	if err := h.db.Table("feedback_answers AS a").
		Joins("JOIN feedback_responses AS r ON r.id = a.response_id").
		Where("r.survey_id = ? AND r.user_id = ?", detail.Survey.ID, fxCheckedID).
		Count(&answerCount).Error; err != nil {
		t.Fatalf("count answers: %v", err)
	}
	if answerCount != int64(len(req.Answers)) {
		t.Fatalf("回答行数应为 %d, got %d", len(req.Answers), answerCount)
	}
}

// ---- 用例 8：问卷、提交与统计在重新连接（刷新页面/重新登录/服务重启）后仍在 ----

func TestFeedbackPersistenceAcrossConnections(t *testing.T) {
	// 步骤：创建并发布问卷、完成提交后关闭连接；重新打开同一数据库文件，
	// 新建服务实例（模拟服务重启 + 用户刷新/重新登录），问卷、提交与统计仍完整可查。
	dbPath := filepath.Join(t.TempDir(), "persist.db")
	db := openFeedbackDBAt(t, dbPath)
	seedFeedbackData(t, db)
	logger := discardLogger()
	actRepo := repository.NewActivityRepository(db)
	regRepo := repository.NewRegistrationRepository(db)
	fbRepo := repository.NewFeedbackRepository(db)
	notifyRepo := repository.NewNotificationRepository(db)
	checkinRepo := repository.NewCheckInRecordRepository(db)
	actSvc := NewActivityService(actRepo, regRepo, notifyRepo, checkinRepo, logger)
	h1 := &feedbackHarness{db: db, svc: NewFeedbackService(db, fbRepo, actSvc, regRepo, logger)}

	detail := createPublishedSurvey(t, h1, fxEndedActivityID)
	if _, err := h1.svc.Submit(fxEndedActivityID, fxCheckedID, validAnswers(detail)); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	sqlDB, _ := db.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// 重新连接：数据来自持久化存储而非内存状态
	h2 := newFeedbackHarnessAtPath(t, dbPath)
	got, err := h2.svc.GetForParticipant(fxEndedActivityID, fxCheckedID)
	if err != nil {
		t.Fatalf("重连后问卷应仍可查询: %v", err)
	}
	if got["submitted"] != true {
		t.Fatalf("重连后 submitted 应为 true")
	}
	if len(got["questions"].([]model.FeedbackQuestion)) != 3 {
		t.Fatalf("重连后题目应完整保留")
	}
	stats, err := h2.svc.Stats(fxEndedActivityID, fxOrganizerID, constants.RoleOrganizer)
	if err != nil {
		t.Fatalf("重连后统计失败: %v", err)
	}
	if stats["response_count"].(int64) != 1 {
		t.Fatalf("重连后填写人数应为 1, got %v", stats["response_count"])
	}
	// 重连后仍不允许重复提交
	if _, err := h2.svc.Submit(fxEndedActivityID, fxCheckedID, validAnswers(detail)); appCode(t, err) != constants.CodeSurveySubmitted {
		t.Fatalf("重连后重复提交仍应被拒绝, got %d", appCode(t, err))
	}
}

// newFeedbackHarnessAtPath 基于已播种数据的指定数据库构造服务（用于持久化测试）。
func newFeedbackHarnessAtPath(t *testing.T, dbPath string) *feedbackHarness {
	t.Helper()
	db := openFeedbackDBAt(t, dbPath)
	logger := discardLogger()
	actRepo := repository.NewActivityRepository(db)
	regRepo := repository.NewRegistrationRepository(db)
	fbRepo := repository.NewFeedbackRepository(db)
	notifyRepo := repository.NewNotificationRepository(db)
	checkinRepo := repository.NewCheckInRecordRepository(db)
	actSvc := NewActivityService(actRepo, regRepo, notifyRepo, checkinRepo, logger)
	return &feedbackHarness{db: db, svc: NewFeedbackService(db, fbRepo, actSvc, regRepo, logger)}
}
