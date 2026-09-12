package router

// 反馈问卷 HTTP 全链路测试 + 原有报名/签到流程回归测试（纯 Go sqlite，无需 MySQL）。
//
// 运行方式：
//
//	cd backend && CGO_ENABLED=0 go test ./internal/router/ -v
//
// 覆盖：
//   - 报名：在线报名成功、重复报名冲突
//   - 签到：组织者凭证号签到成功、重复签到冲突、报名记录随之变为 checked_in
//   - 问卷：未登录 401、普通用户无权创建、未签到不能提交、提交成功、重复提交 409、统计接口
import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gbevent/internal/config"
	"gbevent/internal/constants"
	"gbevent/internal/handler"
	"gbevent/internal/model"
	"gbevent/internal/repository"
	"gbevent/internal/service"
	"gbevent/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type httpEnv struct {
	engine http.Handler
	db     *gorm.DB
	cfg    *config.Config
}

func setupHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	// WAL + busy_timeout + BEGIN IMMEDIATE：支持多个并发 HTTP 请求持有独立物理连接，
	// 写事务在数据库层排队，输家走“已存在提交 → 重复冲突”路径（等价 MySQL FOR UPDATE）。
	dsn := "file:" + filepath.Join(t.TempDir(), "http.db") +
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	// 允许连接池持有多条物理连接，使并发请求真正落在独立连接上。
	sqlDB.SetMaxOpenConns(0)
	if err := db.AutoMigrate(
		&model.User{}, &model.Activity{}, &model.Registration{}, &model.CheckInRecord{},
		&model.Comment{}, &model.Favorite{}, &model.Notification{}, &model.AuditLog{},
		&model.FeedbackSurvey{}, &model.FeedbackQuestion{}, &model.FeedbackResponse{}, &model.FeedbackAnswer{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now()
	fixtures := []any{
		&model.User{ID: 1, Username: "org", Role: constants.RoleOrganizer},
		&model.User{ID: 2, Username: "attendee", Role: constants.RoleUser},
		// 活动 1：已结束（问卷用），活动 2：进行中可报名
		&model.Activity{ID: 1, Title: "已结束活动", ActivityType: "lecture", StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour), SignupDeadline: now.Add(-3 * time.Hour), Status: constants.ActivityStatusEnded, OrganizerID: 1, Capacity: 100},
		&model.Activity{ID: 2, Title: "进行中活动", ActivityType: "lecture", StartTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour), SignupDeadline: now.Add(time.Hour), Status: constants.ActivityStatusPublished, OrganizerID: 1, Capacity: 100},
		// 用户 2 在活动 1 已签到（问卷测试用）
		&model.Registration{ID: 1, ActivityID: 1, UserID: 2, Name: "参加者", Phone: "13900000001", VoucherNo: "FB-CHECKED-001", Status: constants.RegistrationStatusCheckedIn, ReviewStatus: constants.ReviewStatusApproved},
	}
	for _, f := range fixtures {
		if err := db.Create(f).Error; err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpireHours: 72, RateLimitPerMinute: 1000, UploadDir: t.TempDir(), CORSOrigins: []string{"*"}, ServerPort: "8080"}

	userRepo := repository.NewUserRepository(db)
	actRepo := repository.NewActivityRepository(db)
	regRepo := repository.NewRegistrationRepository(db)
	checkinRepo := repository.NewCheckInRecordRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	favRepo := repository.NewFavoriteRepository(db)
	notifyRepo := repository.NewNotificationRepository(db)
	fbRepo := repository.NewFeedbackRepository(db)

	userSvc := service.NewUserService(userRepo, logger)
	actSvc := service.NewActivityService(actRepo, regRepo, notifyRepo, checkinRepo, logger)
	regSvc := service.NewRegistrationService(db, regRepo, actSvc, notifyRepo, logger)
	checkinSvc := service.NewCheckInRecordService(db, checkinRepo, regRepo, actSvc, notifyRepo, logger)
	commentSvc := service.NewCommentService(commentRepo, actSvc, logger)
	favSvc := service.NewFavoriteService(favRepo, actSvc, logger)
	notifySvc := service.NewNotificationService(notifyRepo, logger)
	fbSvc := service.NewFeedbackService(db, fbRepo, actSvc, regRepo, logger)

	r := New(cfg, db, logger,
		handler.NewUserHandler(userSvc, logger),
		handler.NewActivityHandler(actSvc, logger),
		handler.NewRegistrationHandler(regSvc, logger),
		handler.NewCheckInRecordHandler(checkinSvc, logger),
		handler.NewCommentHandler(commentSvc, logger),
		handler.NewFavoriteHandler(favSvc, logger),
		handler.NewNotificationHandler(notifySvc, logger),
		handler.NewFeedbackHandler(fbSvc, logger),
		handler.NewUploadHandler(cfg, logger))
	return &httpEnv{engine: r.Setup(), db: db, cfg: cfg}
}

func (e *httpEnv) token(userID uint64, name, role string) string {
	tok, err := util.GenerateToken(e.cfg.JWTSecret, e.cfg.JWTExpireDuration(), userID, name, role)
	if err != nil {
		panic(err)
	}
	return tok
}

func (e *httpEnv) do(method, path, token string, body any) (int, map[string]any) {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func bodyCode(out map[string]any) int {
	if v, ok := out["code"].(float64); ok {
		return int(v)
	}
	return -999
}

// ---- 原有报名/签到流程回归 ----

func TestExistingSignupAndCheckInFlow(t *testing.T) {
	// 复现步骤：
	//  1. 用户 2（已在活动 1 报名）对进行中的活动 2 在线报名 -> 200/CodeOK，返回凭证号
	//  2. 再次对活动 2 报名 -> 409/CodeDuplicateSignup(40903)
	//  3. 组织者凭证号签到活动 2 -> 200
	//  4. 重复签到同一凭证 -> 409/CodeAlreadyCheckedIn(40904)
	//  5. 报名记录状态变为 checked_in，且生成签到记录与通知
	env := setupHTTPEnv(t)
	userTok := env.token(2, "attendee", constants.RoleUser)
	orgTok := env.token(1, "org", constants.RoleOrganizer)

	signupBody := map[string]any{"activity_id": 2, "name": "参加者", "phone": "13900000001"}
	status, out := env.do(http.MethodPost, "/api/v1/registrations", userTok, signupBody)
	if status != http.StatusOK || bodyCode(out) != constants.CodeOK {
		t.Fatalf("步骤1 报名失败: status=%d body=%v", status, out)
	}
	data := out["data"].(map[string]any)
	voucher, ok := data["voucher_no"].(string)
	if !ok || voucher == "" {
		t.Fatalf("步骤1 报名成功应返回凭证号, got %v", data)
	}

	status, out = env.do(http.MethodPost, "/api/v1/registrations", userTok, signupBody)
	if status != http.StatusConflict || bodyCode(out) != constants.CodeDuplicateSignup {
		t.Fatalf("步骤2 重复报名应 409/%d, got %d/%d", constants.CodeDuplicateSignup, status, bodyCode(out))
	}

	checkinBody := map[string]any{"voucher": voucher}
	status, out = env.do(http.MethodPost, "/api/v1/check-ins?activity_id=2", orgTok, checkinBody)
	if status != http.StatusOK || bodyCode(out) != constants.CodeOK {
		t.Fatalf("步骤3 凭证签到失败: status=%d body=%v", status, out)
	}

	status, _ = env.do(http.MethodPost, "/api/v1/check-ins?activity_id=2", orgTok, checkinBody)
	if status != http.StatusConflict {
		t.Fatalf("步骤4 重复签到应 409, got %d", status)
	}

	// 状态与副作用断言
	var reg model.Registration
	if err := env.db.Where("activity_id = ? AND user_id = ?", 2, 2).First(&reg).Error; err != nil {
		t.Fatalf("查询报名记录: %v", err)
	}
	if reg.Status != constants.RegistrationStatusCheckedIn {
		t.Fatalf("步骤5 签到后报名状态应为 checked_in, got %s", reg.Status)
	}
	var checkinCount int64
	env.db.Model(&model.CheckInRecord{}).Where("activity_id = ?", 2).Count(&checkinCount)
	if checkinCount != 1 {
		t.Fatalf("步骤5 活动 2 应有 1 条签到记录, got %d", checkinCount)
	}
	var notifCount int64
	env.db.Model(&model.Notification{}).Where("user_id = ?", 2).Count(&notifCount)
	if notifCount < 1 {
		t.Fatalf("步骤5 报名/签到应生成通知, got %d 条", notifCount)
	}

	// 评论流程不受影响：活动 2 发表评论并读取
	status, out = env.do(http.MethodPost, "/api/v1/activities/2/comments", userTok, map[string]any{"rating": 4, "content": "不错"})
	if status != http.StatusOK {
		t.Fatalf("发表评论失败: %d %v", status, out)
	}
	status, out = env.do(http.MethodGet, "/api/v1/activities/2/comments", "", nil)
	if status != http.StatusOK || len(out["data"].(map[string]any)["list"].([]any)) != 1 {
		t.Fatalf("评论列表应为 1 条, status=%d", status)
	}
}

// ---- 问卷 HTTP 语义 ----

func TestFeedbackHTTPEndToEnd(t *testing.T) {
	// 复现步骤：
	//  1. 未带令牌 GET 问卷 -> 401
	//  2. 普通用户 POST 创建 -> 403
	//  3. 组织者对已结束活动 1 创建问卷 -> 200；重复创建 -> 409/CodeSurveyExists
	//  4. 发布后，未签到的新用户提交 -> 403/CodeNotCheckedIn
	//  5. 已签到用户（id=2）提交 -> 200；再次提交 -> 409/CodeSurveySubmitted
	//  6. 组织者统计 -> 200，填写人数 1；普通用户访问统计 -> 403
	env := setupHTTPEnv(t)
	orgTok := env.token(1, "org", constants.RoleOrganizer)
	attendeeTok := env.token(2, "attendee", constants.RoleUser)

	// 新注册一个未签到用户
	env.db.Create(&model.User{ID: 3, Username: "other", Role: constants.RoleUser})
	otherTok := env.token(3, "other", constants.RoleUser)

	status, _ := env.do(http.MethodGet, "/api/v1/activities/1/feedback", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("步骤1 未登录应 401, got %d", status)
	}

	createBody := map[string]any{
		"title": "问卷",
		"questions": []map[string]any{
			{"question_type": "radio", "title": "满意度", "options": []string{"满意", "不满意"}, "required": true},
			{"question_type": "checkbox", "title": "环节", "options": []string{"分享", "讨论"}, "required": false},
			{"question_type": "text", "title": "建议", "required": false},
		},
	}
	status, out := env.do(http.MethodPost, "/api/v1/activities/1/feedback", attendeeTok, createBody)
	if status != http.StatusForbidden {
		t.Fatalf("步骤2 普通用户创建应 403, got %d", status)
	}
	status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback", orgTok, createBody)
	if status != http.StatusOK || bodyCode(out) != constants.CodeOK {
		t.Fatalf("步骤3 创建问卷失败: %d %v", status, out)
	}
	status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback", orgTok, createBody)
	if status != http.StatusConflict || bodyCode(out) != constants.CodeSurveyExists {
		t.Fatalf("步骤3 重复创建应 409/%d, got %d/%d", constants.CodeSurveyExists, status, bodyCode(out))
	}

	// 草稿态参加者不可见
	status, _ = env.do(http.MethodGet, "/api/v1/activities/1/feedback", attendeeTok, nil)
	if status != http.StatusConflict {
		t.Fatalf("草稿问卷参加者应收到 409, got %d", status)
	}

	if status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback/publish", orgTok, nil); status != http.StatusOK {
		t.Fatalf("发布失败: %d %v", status, out)
	}

	// 取题目 ID
	_, detail := env.do(http.MethodGet, "/api/v1/activities/1/feedback", attendeeTok, nil)
	questions := detail["data"].(map[string]any)["questions"].([]any)
	qID := func(i int) uint64 { return uint64(questions[i].(map[string]any)["id"].(float64)) }

	submitBody := map[string]any{"answers": []map[string]any{
		{"question_id": qID(0), "values": []string{"满意"}},
		{"question_id": qID(1), "values": []string{"分享"}},
		{"question_id": qID(2), "content": "很好"},
	}}

	// 未签到用户用相同 body 提交
	status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback/submit", otherTok, submitBody)
	if status != http.StatusForbidden || bodyCode(out) != constants.CodeNotCheckedIn {
		t.Fatalf("步骤4 未签到提交应 403/%d, got %d/%d msg=%v", constants.CodeNotCheckedIn, status, bodyCode(out), out["message"])
	}

	status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback/submit", attendeeTok, submitBody)
	if status != http.StatusOK || bodyCode(out) != constants.CodeOK {
		t.Fatalf("步骤5 已签到提交失败: %d %v", status, out)
	}
	status, out = env.do(http.MethodPost, "/api/v1/activities/1/feedback/submit", attendeeTok, submitBody)
	if status != http.StatusConflict || bodyCode(out) != constants.CodeSurveySubmitted {
		t.Fatalf("步骤5 重复提交应 409/%d, got %d/%d", constants.CodeSurveySubmitted, status, bodyCode(out))
	}

	status, out = env.do(http.MethodGet, "/api/v1/activities/1/feedback/stats", orgTok, nil)
	if status != http.StatusOK {
		t.Fatalf("步骤6 统计失败: %d", status)
	}
	statsData := out["data"].(map[string]any)
	if int(statsData["response_count"].(float64)) != 1 {
		t.Fatalf("步骤6 填写人数应为 1, got %v", statsData["response_count"])
	}
	status, _ = env.do(http.MethodGet, "/api/v1/activities/1/feedback/stats", attendeeTok, nil)
	if status != http.StatusForbidden {
		t.Fatalf("步骤6 普通用户不能看统计, got %d", status)
	}
}

// TestFeedbackHTTPConcurrentSubmit 通过真实 Gin 路由并发提交同一问卷：
// 每个请求是独立 httptest 请求，从连接池获得独立物理连接（WAL + BEGIN IMMEDIATE）。
// 预期：恰好 1 个请求 200/CodeOK，其余全部 409/CodeSurveySubmitted；
// feedback_responses 仅 1 行、feedback_answers 仅一组。
func TestFeedbackHTTPConcurrentSubmit(t *testing.T) {
	env := setupHTTPEnv(t)
	orgTok := env.token(1, "org", constants.RoleOrganizer)
	attendeeTok := env.token(2, "attendee", constants.RoleUser)

	createBody := map[string]any{
		"title": "并发问卷",
		"questions": []map[string]any{
			{"question_type": "radio", "title": "满意度", "options": []string{"满意", "不满意"}, "required": true},
			{"question_type": "text", "title": "建议", "required": false},
		},
	}
	if status, out := env.do(http.MethodPost, "/api/v1/activities/1/feedback", orgTok, createBody); status != http.StatusOK {
		t.Fatalf("创建问卷失败: %d %v", status, out)
	}
	if status, _ := env.do(http.MethodPost, "/api/v1/activities/1/feedback/publish", orgTok, nil); status != http.StatusOK {
		t.Fatalf("发布失败: %d", status)
	}
	_, detail := env.do(http.MethodGet, "/api/v1/activities/1/feedback", attendeeTok, nil)
	questions := detail["data"].(map[string]any)["questions"].([]any)
	qID := func(i int) uint64 { return uint64(questions[i].(map[string]any)["id"].(float64)) }
	submitBody := map[string]any{"answers": []map[string]any{
		{"question_id": qID(0), "values": []string{"满意"}},
		{"question_id": qID(1), "content": "很好"},
	}}

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, conflict, other := 0, 0, 0
	statuses := map[int]int{}
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := json.Marshal(submitBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/activities/1/feedback/submit", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+attendeeTok)
			<-start // 栅栏：真正同时发起
			rec := httptest.NewRecorder()
			env.engine.ServeHTTP(rec, req)
			var out map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			mu.Lock()
			defer mu.Unlock()
			code := bodyCode(out)
			statuses[rec.Code]++
			switch {
			case rec.Code == http.StatusOK && code == constants.CodeOK:
				success++
			case rec.Code == http.StatusConflict && code == constants.CodeSurveySubmitted:
				conflict++
			default:
				other++
			}
		}()
	}
	close(start)
	wg.Wait()

	if other != 0 {
		t.Fatalf("并发提交不应出现锁/其他错误, other=%d http状态分布=%v", other, statuses)
	}
	if success != 1 {
		t.Fatalf("应恰好 1 个请求成功, got %d (conflict=%d, http状态=%v)", success, conflict, statuses)
	}
	if conflict != n-1 {
		t.Fatalf("其余 %d 个请求应 409 重复冲突, got %d (http状态=%v)", n-1, conflict, statuses)
	}

	var respCount int64
	if err := env.db.Model(&model.FeedbackResponse{}).Count(&respCount).Error; err != nil {
		t.Fatalf("count responses: %v", err)
	}
	if respCount != 1 {
		t.Fatalf("feedback_responses 必须仅 1 行, got %d", respCount)
	}
	var answerCount int64
	if err := env.db.Model(&model.FeedbackAnswer{}).Count(&answerCount).Error; err != nil {
		t.Fatalf("count answers: %v", err)
	}
	if answerCount != 2 {
		t.Fatalf("feedback_answers 应仅一组 2 行, got %d", answerCount)
	}
}
