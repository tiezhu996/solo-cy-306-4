package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"gbevent/internal/constants"
	"gbevent/internal/dto"
	"gbevent/internal/middleware"
	"gbevent/internal/service"
	"gbevent/internal/util"

	"github.com/gin-gonic/gin"
)

// FeedbackHandler 反馈问卷 HTTP 处理器。
type FeedbackHandler struct {
	svc    *service.FeedbackService
	logger *slog.Logger
}

// NewFeedbackHandler 构造反馈问卷处理器。
func NewFeedbackHandler(svc *service.FeedbackService, logger *slog.Logger) *FeedbackHandler {
	return &FeedbackHandler{svc: svc, logger: logger}
}

// Create 组织者创建问卷。
func (h *FeedbackHandler) Create(c *gin.Context) {
	activityID, ok := parseActivityID(c, "Feedback create")
	if !ok {
		return
	}
	var req dto.FeedbackSurveyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "FeedbackSurvey[activity_id="+strconv.FormatUint(activityID, 10)+"] create: "+err.Error())
		return
	}
	detail, err := h.svc.CreateSurvey(activityID, middleware.GetUserID(c), middleware.GetUserRole(c), req)
	if err != nil {
		h.wrapError(c, err, "Feedback survey create failed")
		return
	}
	OKWithMessage(c, "问卷创建成功", detail)
}

// Publish 组织者发布问卷。
func (h *FeedbackHandler) Publish(c *gin.Context) {
	activityID, ok := parseActivityID(c, "Feedback publish")
	if !ok {
		return
	}
	survey, err := h.svc.PublishSurvey(activityID, middleware.GetUserID(c), middleware.GetUserRole(c))
	if err != nil {
		h.wrapError(c, err, "Feedback survey publish failed")
		return
	}
	OKWithMessage(c, constants.MsgSurveyPublish, survey)
}

// Detail 参加者查看问卷及本人提交状态。
func (h *FeedbackHandler) Detail(c *gin.Context) {
	activityID, ok := parseActivityID(c, "Feedback detail")
	if !ok {
		return
	}
	data, err := h.svc.GetForParticipant(activityID, middleware.GetUserID(c))
	if err != nil {
		h.wrapError(c, err, "Feedback survey detail failed")
		return
	}
	OK(c, data)
}

// Submit 参加者提交问卷。
func (h *FeedbackHandler) Submit(c *gin.Context) {
	activityID, ok := parseActivityID(c, "Feedback submit")
	if !ok {
		return
	}
	var req dto.FeedbackSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "FeedbackSurvey[activity_id="+strconv.FormatUint(activityID, 10)+"] submit: "+err.Error())
		return
	}
	resp, err := h.svc.Submit(activityID, middleware.GetUserID(c), req)
	if err != nil {
		h.wrapError(c, err, "Feedback survey submit failed")
		return
	}
	OKWithMessage(c, constants.MsgSurveySubmit, resp)
}

// Stats 组织者查看问卷统计。
func (h *FeedbackHandler) Stats(c *gin.Context) {
	activityID, ok := parseActivityID(c, "Feedback stats")
	if !ok {
		return
	}
	data, err := h.svc.Stats(activityID, middleware.GetUserID(c), middleware.GetUserRole(c))
	if err != nil {
		h.wrapError(c, err, "Feedback survey stats failed")
		return
	}
	OK(c, data)
}

func parseActivityID(c *gin.Context, ctx string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, ctx+": invalid activity id")
		return 0, false
	}
	return id, true
}

func (h *FeedbackHandler) wrapError(c *gin.Context, err error, ctx string) {
	var appErr *util.AppError
	if errors.As(err, &appErr) {
		c.Set("audit_detail", appErr.Message)
		h.logger.Warn("feedback handler error", "context", ctx, "error", appErr.Error())
		Fail(c, appErrorStatus(appErr.Code), appErr.Code, appErr.Message)
		return
	}
	h.logger.Error("feedback handler error", "context", ctx, "error", err.Error())
	Fail(c, http.StatusInternalServerError, constants.CodeInternalError, constants.MsgInternalError)
}
