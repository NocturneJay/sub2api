package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Images handles OpenAI Images API requests.
// POST /v1/images/generations
// POST /v1/images/edits
func (h *OpenAIGatewayHandler) Images(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.images",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if isMultipartImagesContentType(c.GetHeader("Content-Type")) {
		setOpsRequestContext(c, "", false)
	} else {
		setOpsRequestContext(c, "", false)
	}

	parsed, err := h.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	requestModel := parsed.Model
	ensureCompositeTargetPlatform(c, apiKey, requestModel)
	clientRequestModel := clientRequestedModel(c, requestModel)
	routingModel := requestModel
	if resolvedModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok {
		routingModel = resolvedModel
	}
	imagePlatform := effectiveAPIKeyPlatform(c, apiKey)
	if !compositeTargetPlatformAllowed(c, apiKey, requestModel, service.PlatformOpenAI, service.PlatformGemini) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}
	if imagePlatform == service.PlatformGemini && (h.platformGatewayService == nil || h.geminiCompatService == nil) {
		h.errorResponse(c, http.StatusBadGateway, "api_error", "Gemini image compatibility service is not configured")
		return
	}

	reqLog = reqLog.With(
		zap.String("model", clientRequestModel),
		zap.String("routing_model", routingModel),
		zap.Bool("stream", parsed.Stream),
		zap.Bool("multipart", parsed.Multipart),
		zap.String("capability", string(parsed.RequiredCapability)),
		zap.String("img_quality", parsed.Quality),
		zap.String("img_size", parsed.Size),
	)

	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, parsed.ModerationBody()); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
	if !acquired {
		return
	}
	if imageReleaseFunc != nil {
		defer imageReleaseFunc()
	}

	setOpsRequestContext(c, clientRequestModel, parsed.Stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(parsed.Stream, false)))

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, routingModel)
	// aicat：把渠道映射后的模型名作为选号别名挂进请求 ctx，让调度器与「无可用账号」
	// 错因分类都认它。上游只用映射改写 body，选号仍用原始名，账号白名单写映射后名字时
	// 会直接 404。详见 service/channel_routing_alias.go。
	c.Request = c.Request.WithContext(service.WithChannelRoutingAlias(c.Request.Context(), channelMapping, routingModel))
	schedulingModel := openAIImagesSchedulingModel(routingModel, channelMapping)

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, parsed.Stream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai.images.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
	if imagePlatform == service.PlatformGemini && sessionHash != "" {
		sessionHash = "gemini-images:" + sessionHash
	}
	// 三层都要：ImagesEndpoint 是上游 v0.1.172 新增的来源标记（图片模型被发到
	// Codex 文本端点触发 400 时不再误写模型冷却，靠它区分来源）；
	// ProfitControlSuppressed 是 aicat 侧的——图片路径不装利润门。
	requestCtx := service.WithOpenAIProfitControlSuppressed(
		service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(c.Request.Context())),
	)

	maxAccountSwitches := h.maxAccountSwitches
	if imagePlatform == service.PlatformGemini {
		maxAccountSwitches = h.maxAccountSwitchesGemini
	}
	switchCount := 0
	profitVetoCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	stopJSONKeepalive := func() {}
	jsonKeepaliveStarted := false
	defer func() { stopJSONKeepalive() }()
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	// aicat：保留 Gemini 平台不上报的门（Gemini 图片账号不在 OpenAI 调度器里），
	// 以及 schedulingModel（渠道映射后的调度名，上游用的 requestModel 不含渠道映射）；
	// 模型解析与健康观察（observedErr）采上游 v0.1.182 的新签名。
	reportScheduleResult := func(account *service.Account, result *service.OpenAIForwardResult, success bool, firstTokenMs *int, observedErr ...error) {
		if imagePlatform != service.PlatformGemini && account != nil {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, schedulingModel, false, result), success, firstTokenMs, observedErr...)
		}
	}

	for {
		reqLog.Debug("openai.images.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		var selection *service.AccountSelectionResult
		var scheduleDecision service.OpenAIAccountScheduleDecision
		var err error
		if imagePlatform == service.PlatformGemini {
			selection, err = h.platformGatewayService.SelectAccountWithLoadAwareness(
				requestCtx,
				apiKey.GroupID,
				sessionHash,
				schedulingModel,
				failedAccountIDs,
				"",
				subject.UserID,
			)
			scheduleDecision.Layer = "gemini-load-awareness"
		} else {
			selection, scheduleDecision, err = h.gatewayService.SelectAccountWithSchedulerForImages(
				requestCtx,
				apiKey.GroupID,
				sessionHash,
				schedulingModel,
				failedAccountIDs,
				parsed.RequiredCapability,
			)
		}
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("openai.images.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("openai.images.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				var diagnoser service.ModelAvailabilityDiagnoser = h.gatewayService
				if imagePlatform == service.PlatformGemini {
					diagnoser = h.platformGatewayService
				}
				cls := classifyNoAccountErrorFromGin(c, diagnoser, apiKey, schedulingModel, clientRequestModel, imagePlatform)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				message := cls.Message
				if !cls.ModelNotFound {
					message = "No available compatible accounts"
				}
				h.handleStreamingAwareError(c, cls.Status, cls.ErrType, message, streamStarted)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleFailoverExhaustedSimple(c, 502, streamStarted)
			}
			return
		}
		if selection == nil || selection.Account == nil {
			var diagnoser service.ModelAvailabilityDiagnoser = h.gatewayService
			if imagePlatform == service.PlatformGemini {
				diagnoser = h.platformGatewayService
			}
			cls := classifyNoAccountErrorFromGin(c, diagnoser, apiKey, schedulingModel, clientRequestModel, imagePlatform)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			message := cls.Message
			if !cls.ModelNotFound {
				message = "No available compatible accounts"
			}
			h.handleStreamingAwareError(c, cls.Status, cls.ErrType, message, streamStarted)
			return
		}

		reqLog.Debug("openai.images.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)

		account := selection.Account
		if imagePlatform != service.PlatformGemini {
			sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		}
		reqLog.Debug("openai.images.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
		setOpsSelectedAccount(c, account.ID, account.Platform)
		if imagePlatform == service.PlatformGemini && !service.SupportsGeminiOpenAIImages(account) {
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			failedAccountIDs[account.ID] = struct{}{}
			continue
		}

		var accountReleaseFunc func()
		slotResult := openAISlotAcquireOK
		if imagePlatform == service.PlatformGemini {
			var acquired bool
			accountReleaseFunc, acquired = h.acquireGeminiImagesAccountSlot(c, selection, parsed.Stream, &streamStarted, reqLog)
			if !acquired {
				return
			}
		} else {
			accountReleaseFunc, slotResult = h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, parsed.Stream, &streamStarted, reqLog)
		}
		if slotResult == openAISlotAcquireProfitVetoed {
			// Images 调度不装利润门，此分支实际不可达；防御性排除重选并受同一否决上限约束。
			if !recordOpenAIProfitVeto(failedAccountIDs, account.ID, &profitVetoCount) {
				h.handleOpenAIProfitVetoExhausted(c, streamStarted, reqLog, profitVetoCount)
				return
			}
			continue
		}
		if slotResult != openAISlotAcquireOK {
			return
		}

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		if !parsed.Stream && !jsonKeepaliveStarted {
			stopJSONKeepalive = service.StartOpenAIImagesJSONKeepalive(c, h.openAIImagesJSONKeepaliveInterval())
			jsonKeepaliveStarted = true
		}
		forwardStart := time.Now()
		writerSizeBeforeForward := service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			if imagePlatform == service.PlatformGemini {
				return h.geminiCompatService.ForwardAsOpenAIImages(requestCtx, c, account, parsed, channelMapping.MappedModel)
			}
			return h.gatewayService.ForwardImages(requestCtx, c, account, body, parsed, channelMapping.MappedModel)
		}()
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		if err != nil {
			if result != nil && result.ImageCount > 0 {
				reqLog.Warn("openai.images.forward_partial_error_with_image_result",
					zap.Int64("account_id", account.ID),
					zap.Int("image_count", result.ImageCount),
					zap.Error(err),
				)
			} else {
				var imageUpstreamErr *service.OpenAIImagesUpstreamError
				if errors.As(err, &imageUpstreamErr) {
					retryableServerError := service.IsOpenAIImagesRetryableUpstreamError(imageUpstreamErr)
					if retryableServerError {
						reportScheduleResult(account, result, false, nil, err)
					} else {
						reportScheduleResult(account, result, true, nil)
					}
					logEvent := "openai.images.upstream_user_error"
					if retryableServerError {
						logEvent = "openai.images.upstream_server_error_after_flush"
					}
					reqLog.Warn(logEvent,
						zap.Int64("account_id", account.ID),
						zap.Int("status_code", imageUpstreamErr.StatusCode),
						zap.String("error_type", imageUpstreamErr.ErrorType),
						zap.String("error_code", imageUpstreamErr.Code),
						zap.Error(err),
					)
					return
				}
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					reportScheduleResult(account, result, false, nil, err)
					if service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) != writerSizeBeforeForward {
						reqLog.Warn("openai.images.upstream_failover_skipped_after_flush",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						h.handleFailoverExhausted(c, failoverErr, true)
						return
					}
					if failoverClientGone(c) {
						reqLog.Info("openai.images.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					if failoverErr.RetryableOnSameAccount {
						retryLimit := effectiveSameAccountRetryLimit(failoverErr, account)
						if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
							sameAccountRetryCount[account.ID]++
							retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
							reqLog.Warn("openai.images.pool_mode_same_account_retry",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
								zap.Int("retry_limit", retryLimit),
								zap.Int("retry_count", sameAccountRetryCount[account.ID]),
								zap.Duration("retry_delay", retryDelay),
							)
							select {
							case <-requestCtx.Done():
								return
							case <-time.After(retryDelay):
							}
							continue
						}
					}
					if imagePlatform != service.PlatformGemini {
						h.gatewayService.RecordOpenAIAccountSwitch()
					}
					failedAccountIDs[account.ID] = struct{}{}
					lastFailoverErr = failoverErr
					if switchCount >= maxAccountSwitches {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					switchCount++
					if imagePlatform != service.PlatformGemini && h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					reqLog.Warn("openai.images.upstream_failover_switching",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					)
					continue
				}
				reportScheduleResult(account, result, false, nil, err)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
				wroteFallback := false
				if !upstreamErrorAlreadyCommunicated {
					wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
				}
				fields := []zap.Field{
					zap.Int64("account_id", account.ID),
					zap.Bool("fallback_error_response_written", wroteFallback),
					zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
					zap.Error(err),
				}
				if shouldLogOpenAIForwardFailureAsWarn(c, wroteFallback) {
					reqLog.Warn("openai.images.forward_failed", fields...)
					return
				}
				reqLog.Error("openai.images.forward_failed", fields...)
				return
			}
		}
		if result != nil {
			// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
			if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
				h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
			}
			reportScheduleResult(account, result, true, result.FirstTokenMs)
		} else {
			reportScheduleResult(account, result, true, nil)
		}

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		if parsed.Multipart {
			requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
		}
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)

		upstreamModel := ""
		if result != nil {
			upstreamModel = result.UpstreamModel
		}
		sessionID := service.ExtractClientSessionID(c)
		h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
				Result:             result,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				QuotaPlatform:      quotaPlatform,
				SessionID:          sessionID,
				ChannelUsageFields: clientRequestedUsageFields(c, channelMapping, requestModel, upstreamModel),
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.openai_gateway.images"),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", clientRequestModel),
					zap.Int64("account_id", account.ID),
				).Error("openai.images.record_usage_failed", zap.Error(err))
			}
		})

		reqLog.Debug("openai.images.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func (h *OpenAIGatewayHandler) openAIImagesJSONKeepaliveInterval() time.Duration {
	if h.cfg == nil || h.cfg.Gateway.ImageNonstreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(h.cfg.Gateway.ImageNonstreamKeepaliveInterval) * time.Second
}

func isMultipartImagesContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/form-data")
}

func openAIImagesSchedulingModel(routingModel string, mapping service.ChannelMappingResult) string {
	if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
		return mapped
	}
	return strings.TrimSpace(routingModel)
}

func (h *OpenAIGatewayHandler) acquireGeminiImagesAccountSlot(
	c *gin.Context,
	selection *service.AccountSelectionResult,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
) (func(), bool) {
	if selection == nil || selection.Account == nil {
		markOpsRoutingCapacityLimited(c)
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", *streamStarted)
		return nil, false
	}
	if selection.Acquired {
		return wrapReleaseOnDone(c.Request.Context(), selection.ReleaseFunc), true
	}
	if selection.WaitPlan == nil {
		markOpsRoutingCapacityLimited(c)
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", *streamStarted)
		return nil, false
	}
	waitCounted := false
	canWait, waitErr := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), selection.Account.ID, selection.WaitPlan.MaxWaiting)
	if waitErr != nil {
		reqLog.Warn("openai.images.gemini_account_wait_counter_increment_failed", zap.Int64("account_id", selection.Account.ID), zap.Error(waitErr))
	} else if !canWait {
		reqLog.Info("openai.images.gemini_account_wait_queue_full",
			zap.Int64("account_id", selection.Account.ID),
			zap.Int("max_waiting", selection.WaitPlan.MaxWaiting),
		)
		h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", *streamStarted)
		return nil, false
	} else {
		waitCounted = true
	}
	releaseWait := func() {
		if waitCounted {
			h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), selection.Account.ID)
			waitCounted = false
		}
	}
	defer releaseWait()
	release, err := h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
		c,
		selection.Account.ID,
		selection.WaitPlan.MaxConcurrency,
		selection.WaitPlan.Timeout,
		reqStream,
		streamStarted,
	)
	if err != nil {
		reqLog.Warn("openai.images.gemini_account_slot_acquire_failed", zap.Int64("account_id", selection.Account.ID), zap.Error(err))
		h.handleConcurrencyError(c, err, "account", *streamStarted)
		return nil, false
	}
	releaseWait()
	return wrapReleaseOnDone(c.Request.Context(), release), true
}
