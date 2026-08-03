package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

const geminiOpenAIImagesMaxN = 10

type geminiOpenAIImage struct {
	B64JSON       string
	MIMEType      string
	RevisedPrompt string
}

type geminiOpenAIImagesResponse struct {
	ResponseID     string `json:"responseId"`
	PromptFeedback *struct {
		BlockReason        string `json:"blockReason"`
		BlockReasonMessage string `json:"blockReasonMessage"`
	} `json:"promptFeedback"`
	Candidates []struct {
		FinishReason  string `json:"finishReason"`
		FinishMessage string `json:"finishMessage"`
		Content       struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type geminiOpenAIImagesContentPolicyError struct {
	reason  string
	message string
}

func (e *geminiOpenAIImagesContentPolicyError) Error() string {
	if e == nil {
		return "Gemini image generation was blocked by content policy"
	}
	if message := strings.TrimSpace(e.message); message != "" {
		return sanitizeUpstreamErrorMessage(message)
	}
	if reason := strings.TrimSpace(e.reason); reason != "" {
		return fmt.Sprintf("Gemini image generation was blocked by content policy (%s)", reason)
	}
	return "Gemini image generation was blocked by content policy"
}

// SupportsGeminiOpenAIImages reports whether an account can serve the OpenAI
// Images compatibility surface through Vertex generateContent.
func SupportsGeminiOpenAIImages(account *Account) bool {
	return account != nil && account.Platform == PlatformGemini && account.Type == AccountTypeServiceAccount
}

// ForwardAsOpenAIImages translates an OpenAI Images JSON request into one or
// more Vertex generateContent calls and writes an OpenAI Images JSON response.
func (s *GeminiMessagesCompatService) ForwardAsOpenAIImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if s == nil || s.tokenProvider == nil || s.httpUpstream == nil {
		return nil, errors.New("gemini images compatibility service is not configured")
	}
	if !SupportsGeminiOpenAIImages(account) {
		return nil, fmt.Errorf("unsupported Gemini images account")
	}
	if parsed == nil {
		return nil, errors.New("parsed images request is required")
	}
	if parsed.Endpoint != openAIImagesGenerationsEndpoint && parsed.Endpoint != openAIImagesEditsEndpoint {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", "unsupported images endpoint")
	}
	if parsed.Stream {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", "Streaming is not supported for Gemini image compatibility")
	}
	if parsed.HasMask || strings.TrimSpace(parsed.MaskImageURL) != "" || parsed.MaskUpload != nil {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", "mask editing is not supported for Gemini image compatibility")
	}
	if strings.TrimSpace(parsed.Prompt) == "" {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", "prompt is required")
	}
	if parsed.N < 1 || parsed.N > geminiOpenAIImagesMaxN {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("n must be between 1 and %d", geminiOpenAIImagesMaxN))
	}

	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	upstreamModel := strings.TrimSpace(account.GetMappedModel(requestModel))
	if upstreamModel == "" {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", "mapped Gemini image model is empty")
	}

	generateBody, effectiveSize, err := buildGeminiOpenAIImagesRequestBody(parsed, upstreamModel)
	if err != nil {
		return s.writeGeminiOpenAIImagesClientError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	_, expectedOutputMIME, _ := geminiOpenAIImagesOutputOptions(parsed)

	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:       http.StatusUnauthorized,
			ResponseBody:     []byte(`{"error":{"message":"Vertex service account authentication failed"}}`),
			Stage:            GatewayFailureStageAccountAuth,
			Scope:            GatewayFailureScopeAccount,
			ClientStatusCode: http.StatusBadGateway,
			ClientMessage:    "Vertex service account authentication failed",
		}
	}
	fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(upstreamModel), upstreamModel, "generateContent", false)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:       http.StatusBadGateway,
			ResponseBody:     []byte(fmt.Sprintf(`{"error":{"message":%q}}`, sanitizeUpstreamErrorMessage(err.Error()))),
			Stage:            GatewayFailureStageAccountAuth,
			Scope:            GatewayFailureScopeAccount,
			ClientStatusCode: http.StatusBadGateway,
			ClientMessage:    "Invalid Vertex account configuration",
		}
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	images := make([]geminiOpenAIImage, 0, parsed.N)
	usage := OpenAIUsage{}
	requestID := ""
	var responseHeaders http.Header
	upstreamStart := time.Now()
	for len(images) < parsed.N {
		resp, requestErr := s.doGeminiOpenAIImagesRequest(ctx, c, account, fullURL, accessToken, proxyURL, upstreamModel, expectedOutputMIME, generateBody)
		if requestErr != nil {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			if len(images) > 0 {
				// Some calls in this n request have already consumed upstream quota.
				// Return and bill the completed images instead of failing over and
				// generating them a second time on another account.
				result := geminiOpenAIImagesForwardResult(requestID, requestModel, upstreamModel, responseHeaders, startTime, usage, len(images), effectiveSize)
				s.writeGeminiOpenAIImagesSuccess(c, images, parsed.ResponseFormat, usage, responseHeaders, requestID)
				return result, nil
			}
			var upstreamErr *OpenAIImagesUpstreamError
			if errors.As(requestErr, &upstreamErr) {
				writeOpenAIImagesUpstreamErrorResponse(c, upstreamErr)
			}
			return nil, requestErr
		}

		if requestID == "" {
			requestID = resp.requestID
		}
		if responseHeaders == nil {
			responseHeaders = resp.headers
		}
		mergeGeminiOpenAIImagesUsage(&usage, resp.usage)
		remaining := parsed.N - len(images)
		if len(resp.images) > remaining {
			resp.images = resp.images[:remaining]
		}
		images = append(images, resp.images...)
		if len(resp.images) == 0 {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			if len(images) > 0 {
				result := geminiOpenAIImagesForwardResult(requestID, requestModel, upstreamModel, responseHeaders, startTime, usage, len(images), effectiveSize)
				s.writeGeminiOpenAIImagesSuccess(c, images, parsed.ResponseFormat, usage, responseHeaders, requestID)
				return result, nil
			}
			if strings.TrimSpace(resp.text) != "" {
				upstreamErr := &OpenAIImagesUpstreamError{
					StatusCode:        http.StatusBadRequest,
					ErrorType:         "image_generation_user_error",
					Code:              "content_policy_violation",
					Message:           sanitizeUpstreamErrorMessage(strings.TrimSpace(resp.text)),
					UpstreamRequestID: resp.requestID,
				}
				writeOpenAIImagesUpstreamErrorResponse(c, upstreamErr)
				return nil, upstreamErr
			}
			return nil, &UpstreamFailoverError{
				StatusCode:       http.StatusBadGateway,
				ResponseBody:     []byte(`{"error":{"message":"Gemini returned no image data"}}`),
				Stage:            GatewayFailureStageInference,
				Scope:            GatewayFailureScopeAccount,
				ClientStatusCode: http.StatusBadGateway,
				ClientMessage:    "Gemini returned no image data",
			}
		}
	}
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())

	s.writeGeminiOpenAIImagesSuccess(c, images, parsed.ResponseFormat, usage, responseHeaders, requestID)
	return geminiOpenAIImagesForwardResult(requestID, requestModel, upstreamModel, responseHeaders, startTime, usage, len(images), effectiveSize), nil
}

type geminiOpenAIImagesAttempt struct {
	images    []geminiOpenAIImage
	text      string
	usage     *ClaudeUsage
	requestID string
	headers   http.Header
}

func (s *GeminiMessagesCompatService) doGeminiOpenAIImagesRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	fullURL string,
	accessToken string,
	proxyURL string,
	upstreamModel string,
	expectedOutputMIME string,
	body []byte,
) (*geminiOpenAIImagesAttempt, error) {
	var resp *http.Response
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
				Kind: "request_error", Message: safeErr,
			})
			if attempt < geminiMaxRetries {
				sleepGeminiBackoff(attempt)
				continue
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			return nil, &UpstreamFailoverError{
				StatusCode: http.StatusBadGateway, ResponseBody: []byte(safeErr),
				Stage: GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
			}
		}

		if matched, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp, upstreamModel); matched {
			resp = rebuilt
			break
		} else {
			resp = rebuilt
		}
		if resp.StatusCode >= http.StatusBadRequest && s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			if attempt < geminiMaxRetries {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  firstGeminiRequestID(resp.Header),
					Kind:               "retry", Message: sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody))),
				})
				sleepGeminiBackoff(attempt)
				continue
			}
			resp = &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(respBody))}
		}
		break
	}
	if resp == nil {
		return nil, errors.New("missing Gemini upstream response")
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := firstGeminiRequestID(resp.Header)
	if resp.StatusCode >= http.StatusBadRequest {
		respBody := s.readUpstreamErrorBody(resp)
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: requestID,
			Kind: "http_error", Message: upstreamMsg,
		})
		s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		if s.shouldFailoverGeminiUpstreamError(resp.StatusCode) {
			return nil, &UpstreamFailoverError{
				StatusCode: resp.StatusCode, ResponseBody: respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
				Stage:                  GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
			}
		}
		upstreamErr := openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, respBody)
		upstreamErr.UpstreamRequestID = requestID
		return nil, upstreamErr
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if requestID == "" {
		requestID = geminiOpenAIImagesResponseID(respBody)
	}
	images, text, err := extractGeminiOpenAIImagesWithExpectedMIME(respBody, expectedOutputMIME)
	if err != nil {
		var policyErr *geminiOpenAIImagesContentPolicyError
		if errors.As(err, &policyErr) {
			return nil, &OpenAIImagesUpstreamError{
				StatusCode: http.StatusBadRequest, ErrorType: "content_policy_violation", Code: "content_policy_violation",
				Message: policyErr.Error(), UpstreamRequestID: requestID,
			}
		}
		safeMessage := sanitizeUpstreamErrorMessage(err.Error())
		return nil, &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway, ResponseBody: []byte(safeMessage),
			Stage: GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
			ClientStatusCode: http.StatusBadGateway, ClientMessage: safeMessage,
		}
	}
	return &geminiOpenAIImagesAttempt{
		images: images, text: text, usage: extractGeminiUsage(respBody),
		requestID: requestID, headers: resp.Header.Clone(),
	}, nil
}

func geminiOpenAIImagesResponseID(body []byte) string {
	var decoded geminiOpenAIImagesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.ResponseID)
}

func buildGeminiOpenAIImagesGenerateBody(prompt, size, upstreamModel string) ([]byte, string, error) {
	return buildGeminiOpenAIImagesRequestBody(&OpenAIImagesRequest{Prompt: prompt, Size: size}, upstreamModel)
}

func buildGeminiOpenAIImagesRequestBody(parsed *OpenAIImagesRequest, upstreamModel string) ([]byte, string, error) {
	if parsed == nil {
		return nil, "", errors.New("parsed images request is required")
	}
	prompt := strings.TrimSpace(parsed.Prompt)
	if prompt == "" {
		return nil, "", errors.New("prompt is required")
	}
	parts, err := geminiOpenAIImagesRequestParts(parsed)
	if err != nil {
		return nil, "", err
	}
	imageSize := geminiOpenAIImagesRequestedSize(parsed.Size)
	if isGeminiFlashLiteImageModel(upstreamModel) {
		imageSize = ImageBillingSize1K
	}
	imageConfig := map[string]any{"imageSize": imageSize}
	if aspectRatio := nearestGeminiImageAspectRatio(parsed.Size); aspectRatio != "" {
		imageConfig["aspectRatio"] = aspectRatio
	}
	outputOptions, _, err := geminiOpenAIImagesOutputOptions(parsed)
	if err != nil {
		return nil, "", err
	}
	if outputOptions != nil {
		imageConfig["imageOutputOptions"] = outputOptions
	}
	payload := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": parts,
		}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
			"imageConfig":        imageConfig,
		},
	}
	body, err := json.Marshal(payload)
	return body, imageSize, err
}

func geminiOpenAIImagesOutputOptions(parsed *OpenAIImagesRequest) (map[string]any, string, error) {
	if parsed == nil {
		return nil, "", errors.New("parsed images request is required")
	}
	format := strings.ToLower(strings.TrimSpace(parsed.OutputFormat))
	if format == "jpg" {
		format = "jpeg"
	}
	mimeType := ""
	switch format {
	case "":
	case "png":
		mimeType = "image/png"
	case "jpeg":
		mimeType = "image/jpeg"
	case "webp":
		mimeType = "image/webp"
	default:
		return nil, "", fmt.Errorf("unsupported output_format %q (use png, jpeg, or webp)", parsed.OutputFormat)
	}
	if parsed.OutputCompression != nil && mimeType == "image/jpeg" {
		quality := *parsed.OutputCompression
		if quality < 0 || quality > 100 {
			return nil, "", errors.New("output_compression must be between 0 and 100")
		}
	}
	if mimeType == "" {
		return nil, "", nil
	}
	options := map[string]any{"mimeType": mimeType}
	if parsed.OutputCompression != nil && mimeType == "image/jpeg" {
		options["compressionQuality"] = *parsed.OutputCompression
	}
	return options, mimeType, nil
}

func geminiOpenAIImagesRequestedSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "", "auto":
		return ImageBillingSize1K
	case "1k":
		return ImageBillingSize1K
	case "2k":
		return ImageBillingSize2K
	case "4k":
		return ImageBillingSize4K
	default:
		width, height, ok := parseImageBillingDimensions(size)
		if !ok {
			return ImageBillingSize1K
		}
		// The AICat size presets are tiered by pixel budget, not longest edge.
		// In particular 1024x1536 and 1536x1024 are both 1K presets.
		pixels := int64(width) * int64(height)
		switch {
		case pixels <= 1_572_864:
			return ImageBillingSize1K
		case pixels <= 4_194_304:
			return ImageBillingSize2K
		default:
			return ImageBillingSize4K
		}
	}
}

func geminiOpenAIImagesRequestParts(parsed *OpenAIImagesRequest) ([]any, error) {
	if parsed.HasMask || strings.TrimSpace(parsed.MaskImageURL) != "" || parsed.MaskUpload != nil {
		return nil, errors.New("mask editing is not supported")
	}
	parts := []any{map[string]any{"text": strings.TrimSpace(parsed.Prompt)}}
	for _, imageURL := range parsed.InputImageURLs {
		part, err := geminiOpenAIImagesURLPart(imageURL)
		if err != nil {
			return nil, fmt.Errorf("invalid reference image: %w", err)
		}
		parts = append(parts, part)
	}
	for _, upload := range parsed.Uploads {
		part, err := geminiOpenAIImagesUploadPart(upload)
		if err != nil {
			return nil, fmt.Errorf("invalid reference image %q: %w", upload.FileName, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func geminiOpenAIImagesUploadPart(upload OpenAIImagesUpload) (map[string]any, error) {
	if len(upload.Data) == 0 {
		return nil, errors.New("image data is empty")
	}
	mimeType := normalizeGeminiOpenAIImagesInputMIME(upload.ContentType)
	if detected := normalizeGeminiOpenAIImagesInputMIME(http.DetectContentType(upload.Data)); detected != "" {
		mimeType = detected
	}
	if mimeType == "" {
		return nil, errors.New("only PNG, JPEG, WEBP, and GIF images are supported")
	}
	return map[string]any{"inlineData": map[string]any{
		"mimeType": mimeType,
		"data":     base64.StdEncoding.EncodeToString(upload.Data),
	}}, nil
}

func geminiOpenAIImagesURLPart(rawURL string) (map[string]any, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("image URL is empty")
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		mimeType, data, err := decodeGeminiOpenAIImagesDataURL(rawURL)
		if err != nil {
			return nil, err
		}
		return map[string]any{"inlineData": map[string]any{
			"mimeType": mimeType,
			"data":     base64.StdEncoding.EncodeToString(data),
		}}, nil
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" && !strings.EqualFold(parsedURL.Scheme, "gs") {
		return nil, errors.New("image URL is invalid")
	}
	switch strings.ToLower(parsedURL.Scheme) {
	case "https", "http", "gs":
	default:
		return nil, fmt.Errorf("image URL scheme %q is not supported", parsedURL.Scheme)
	}
	fileData := map[string]any{"fileUri": rawURL}
	if mimeType := normalizeGeminiOpenAIImagesInputMIME(mime.TypeByExtension(path.Ext(parsedURL.Path))); mimeType != "" {
		fileData["mimeType"] = mimeType
	}
	return map[string]any{"fileData": fileData}, nil
}

func decodeGeminiOpenAIImagesDataURL(rawURL string) (string, []byte, error) {
	header, payload, ok := strings.Cut(rawURL[len("data:"):], ",")
	if !ok {
		return "", nil, errors.New("data URL is missing a comma separator")
	}
	headerParts := strings.Split(header, ";")
	if len(headerParts) < 2 || !strings.EqualFold(strings.TrimSpace(headerParts[len(headerParts)-1]), "base64") {
		return "", nil, errors.New("data URL must be base64 encoded")
	}
	mediaType, _, err := mime.ParseMediaType(strings.Join(headerParts[:len(headerParts)-1], ";"))
	if err != nil {
		return "", nil, fmt.Errorf("invalid data URL media type: %w", err)
	}
	mediaType = normalizeGeminiOpenAIImagesInputMIME(mediaType)
	if mediaType == "" {
		return "", nil, errors.New("only PNG, JPEG, WEBP, and GIF data URLs are supported")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", nil, fmt.Errorf("invalid data URL base64 payload: %w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("data URL image is empty")
	}
	if detected := normalizeGeminiOpenAIImagesInputMIME(http.DetectContentType(data)); detected != "" {
		mediaType = detected
	}
	return mediaType, data, nil
}

func normalizeGeminiOpenAIImagesInputMIME(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(value))
	}
	switch strings.ToLower(mediaType) {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png", "image/webp", "image/gif":
		return strings.ToLower(mediaType)
	default:
		return ""
	}
}

func isGeminiFlashLiteImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}
	return model == "gemini-3.1-flash-lite-image"
}

func nearestGeminiImageAspectRatio(size string) string {
	trimmed := strings.ToLower(strings.TrimSpace(size))
	for _, ratio := range []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"} {
		if trimmed == ratio {
			return ratio
		}
	}
	width, height, ok := parseImageBillingDimensions(trimmed)
	if !ok {
		return ""
	}
	target := float64(width) / float64(height)
	type ratioCandidate struct {
		name  string
		value float64
	}
	candidates := []ratioCandidate{
		{"1:1", 1}, {"2:3", 2.0 / 3}, {"3:2", 3.0 / 2}, {"3:4", 3.0 / 4},
		{"4:3", 4.0 / 3}, {"4:5", 4.0 / 5}, {"5:4", 5.0 / 4}, {"9:16", 9.0 / 16},
		{"16:9", 16.0 / 9}, {"21:9", 21.0 / 9},
	}
	best := candidates[0]
	bestDistance := math.Abs(math.Log(target / best.value))
	for _, candidate := range candidates[1:] {
		distance := math.Abs(math.Log(target / candidate.value))
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best.name
}

func extractGeminiOpenAIImages(body []byte) ([]geminiOpenAIImage, string, error) {
	return extractGeminiOpenAIImagesWithExpectedMIME(body, "")
}

func extractGeminiOpenAIImagesWithExpectedMIME(body []byte, expectedMIME string) ([]geminiOpenAIImage, string, error) {
	var decoded geminiOpenAIImagesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, "", fmt.Errorf("decode Gemini image response: %w", err)
	}
	if decoded.PromptFeedback != nil {
		reason := strings.ToUpper(strings.TrimSpace(decoded.PromptFeedback.BlockReason))
		if reason != "" && reason != "BLOCK_REASON_UNSPECIFIED" {
			return nil, "", &geminiOpenAIImagesContentPolicyError{reason: reason, message: decoded.PromptFeedback.BlockReasonMessage}
		}
	}
	for _, candidate := range decoded.Candidates {
		if reason := strings.ToUpper(strings.TrimSpace(candidate.FinishReason)); isGeminiOpenAIImagesPolicyFinishReason(reason) {
			return nil, "", &geminiOpenAIImagesContentPolicyError{reason: reason, message: candidate.FinishMessage}
		}
	}
	images := make([]geminiOpenAIImage, 0, len(decoded.Candidates))
	allText := make([]string, 0, len(decoded.Candidates))
	for _, candidate := range decoded.Candidates {
		candidateTextParts := make([]string, 0, len(candidate.Content.Parts))
		for _, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				candidateTextParts = append(candidateTextParts, text)
			}
		}
		candidateText := strings.Join(candidateTextParts, "\n")
		if candidateText != "" {
			allText = append(allText, candidateText)
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil {
				continue
			}
			rawMIMEType := strings.TrimSpace(part.InlineData.MIMEType)
			mimeType := normalizeGeminiOpenAIImagesInputMIME(rawMIMEType)
			if rawMIMEType == "" {
				mimeType = "image/png"
			}
			if !isGeminiInlineImageMIMEType(mimeType) {
				continue
			}
			if expectedMIME != "" && mimeType != expectedMIME {
				return nil, strings.Join(allText, "\n"), fmt.Errorf("Gemini returned %s image data for requested %s output", mimeType, expectedMIME)
			}
			b64 := normalizeOpenAIImageBase64(part.InlineData.Data)
			if b64 == "" {
				continue
			}
			images = append(images, geminiOpenAIImage{B64JSON: b64, MIMEType: mimeType, RevisedPrompt: candidateText})
		}
	}
	return images, strings.Join(allText, "\n"), nil
}

func isGeminiOpenAIImagesPolicyFinishReason(reason string) bool {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "RECITATION", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION":
		return true
	default:
		return false
	}
}

func mergeGeminiOpenAIImagesUsage(dst *OpenAIUsage, src *ClaudeUsage) {
	if dst == nil || src == nil {
		return
	}
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
	dst.ImageOutputTokens += src.ImageOutputTokens
}

func firstGeminiRequestID(header http.Header) string {
	if header == nil {
		return ""
	}
	if requestID := strings.TrimSpace(header.Get("x-request-id")); requestID != "" {
		return requestID
	}
	return strings.TrimSpace(header.Get("x-goog-request-id"))
}

func geminiOpenAIImagesForwardResult(
	requestID, requestModel, upstreamModel string,
	headers http.Header,
	startTime time.Time,
	usage OpenAIUsage,
	imageCount int,
	imageSize string,
) *OpenAIForwardResult {
	outputSizes := make([]string, imageCount)
	for i := range outputSizes {
		outputSizes[i] = imageSize
	}
	return &OpenAIForwardResult{
		RequestID: requestID, Usage: usage, Model: requestModel, UpstreamModel: upstreamModel, BillingModel: upstreamModel,
		Stream: false, ResponseHeaders: headers, Duration: time.Since(startTime),
		ImageCount: imageCount, ImageSize: imageSize, ImageInputSize: imageSize, ImageOutputSizes: outputSizes,
	}
}

func (s *GeminiMessagesCompatService) writeGeminiOpenAIImagesSuccess(
	c *gin.Context,
	images []geminiOpenAIImage,
	responseFormat string,
	usage OpenAIUsage,
	headers http.Header,
	requestID string,
) {
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), headers, s.responseHeaderFilter)
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}
	data := make([]map[string]any, 0, len(images))
	for _, image := range images {
		item := map[string]any{}
		if strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
			item["url"] = "data:" + image.MIMEType + ";base64," + image.B64JSON
		} else {
			item["b64_json"] = image.B64JSON
		}
		if image.RevisedPrompt != "" {
			item["revised_prompt"] = image.RevisedPrompt
		}
		data = append(data, item)
	}
	c.JSON(http.StatusOK, map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
		"usage": map[string]int{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens,
		},
	})
}

func (s *GeminiMessagesCompatService) writeGeminiOpenAIImagesClientError(
	c *gin.Context,
	status int,
	errType string,
	message string,
) (*OpenAIForwardResult, error) {
	upstreamErr := &OpenAIImagesUpstreamError{StatusCode: status, ErrorType: errType, Message: message}
	writeOpenAIImagesUpstreamErrorResponse(c, upstreamErr)
	return nil, upstreamErr
}
