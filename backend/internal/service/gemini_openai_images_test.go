package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type fixedGeminiTokenCache struct {
	token string
}

func (c *fixedGeminiTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return c.token, nil
}

func (c *fixedGeminiTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (c *fixedGeminiTokenCache) DeleteAccessToken(context.Context, string) error { return nil }

func (c *fixedGeminiTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (c *fixedGeminiTokenCache) ReleaseRefreshLock(context.Context, string) error { return nil }

type geminiImagesHTTPUpstreamStub struct {
	calls          int
	bodies         [][]byte
	request        *http.Request
	responseStatus []int
	responseMIME   []string
	responseBodies []string
	omitRequestID  bool
}

func (s *geminiImagesHTTPUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	s.request = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.bodies = append(s.bodies, body)
	status := http.StatusOK
	if index := s.calls - 1; index < len(s.responseStatus) {
		status = s.responseStatus[index]
	}
	mimeType := "image/png"
	if index := s.calls - 1; index < len(s.responseMIME) && strings.TrimSpace(s.responseMIME[index]) != "" {
		mimeType = s.responseMIME[index]
	}
	responseBody := `{
		"candidates":[{"content":{"parts":[
			{"text":"refined prompt"},
			{"inlineData":{"mimeType":"` + mimeType + `","data":"aW1hZ2U="}}
		]}}],
		"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":1290}]}
	}`
	if index := s.calls - 1; index < len(s.responseBodies) && strings.TrimSpace(s.responseBodies[index]) != "" {
		responseBody = s.responseBodies[index]
	}
	if status >= http.StatusBadRequest {
		responseBody = `{"error":{"code":` + fmt.Sprint(status) + `,"message":"account unavailable","status":"UNAUTHENTICATED"}}`
	}
	headers := http.Header{"Content-Type": []string{"application/json"}}
	if !s.omitRequestID {
		headers.Set("X-Goog-Request-Id", "vertex-request")
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(bytes.NewBufferString(responseBody)),
	}, nil
}

func (s *geminiImagesHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestBuildGeminiOpenAIImagesGenerateBodyMapsDimensions(t *testing.T) {
	body, imageSize, err := buildGeminiOpenAIImagesGenerateBody("draw a panorama", "3840x1648", "gemini-3-pro-image")
	require.NoError(t, err)
	require.Equal(t, ImageBillingSize4K, imageSize)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	configMap := payload["generationConfig"].(map[string]any)
	require.Equal(t, []any{"TEXT", "IMAGE"}, configMap["responseModalities"])
	imageConfig := configMap["imageConfig"].(map[string]any)
	require.Equal(t, "21:9", imageConfig["aspectRatio"])
	require.Equal(t, "4K", imageConfig["imageSize"])
}

func TestValidateOpenAIImagesModelAcceptsAICatAliases(t *testing.T) {
	for _, model := range []string{
		"image-2",
		"Nano-Banana-2",
		"Nano-Banana-2-Lite",
		"Nano-Banana-Pro",
		"gemini-3-pro-image",
	} {
		require.NoError(t, validateOpenAIImagesModel(model), model)
	}
	require.Error(t, validateOpenAIImagesModel("gemini-3.6-flash"))
}

func TestExtractGeminiOpenAIImages(t *testing.T) {
	images, text, err := extractGeminiOpenAIImages([]byte(`{
		"candidates":[{"content":{"parts":[
			{"text":"refined prompt"},
			{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}
		]}}]
	}`))
	require.NoError(t, err)
	require.Equal(t, "refined prompt", text)
	require.Len(t, images, 1)
	require.Equal(t, "aW1hZ2U=", images[0].B64JSON)
}

func TestGeminiForwardAsOpenAIImagesUsesResponseIDFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiImagesHTTPUpstreamStub{
		omitRequestID: true,
		responseBodies: []string{`{
			"responseId":"body-response-id",
			"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}}]
		}`},
	}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream, cfg: &config.Config{},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
	result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    "Nano-Banana-Pro", Prompt: "draw", N: 1,
	}, "")
	require.NoError(t, err)
	require.Equal(t, "body-response-id", result.RequestID)
	require.Equal(t, "body-response-id", recorder.Header().Get("x-request-id"))
}

func TestSupportsGeminiOpenAIImagesRequiresVertexServiceAccount(t *testing.T) {
	require.True(t, SupportsGeminiOpenAIImages(&Account{Platform: PlatformGemini, Type: AccountTypeServiceAccount}))
	require.False(t, SupportsGeminiOpenAIImages(&Account{Platform: PlatformGemini, Type: AccountTypeAPIKey}))
	require.False(t, SupportsGeminiOpenAIImages(&Account{Platform: PlatformOpenAI, Type: AccountTypeServiceAccount}))
}

func TestBuildGeminiOpenAIImagesRequestBodyMapsOutputOptions(t *testing.T) {
	quality := 82
	tests := []struct {
		name        string
		format      string
		compression *int
		wantMIME    string
		wantQuality any
	}{
		{name: "png", format: "png", wantMIME: "image/png"},
		{name: "jpeg with quality", format: "jpeg", compression: &quality, wantMIME: "image/jpeg", wantQuality: float64(82)},
		{name: "jpg alias", format: "jpg", wantMIME: "image/jpeg"},
		{name: "webp ignores compression", format: "webp", compression: &quality, wantMIME: "image/webp"},
		{name: "png ignores compression", format: "png", compression: &quality, wantMIME: "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _, err := buildGeminiOpenAIImagesRequestBody(&OpenAIImagesRequest{
				Prompt: "draw", Size: "auto", OutputFormat: tt.format, OutputCompression: tt.compression,
			}, "gemini-3-pro-image")
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			configMap := payload["generationConfig"].(map[string]any)
			imageConfig := configMap["imageConfig"].(map[string]any)
			options := imageConfig["imageOutputOptions"].(map[string]any)
			require.Equal(t, tt.wantMIME, options["mimeType"])
			require.Equal(t, tt.wantQuality, options["compressionQuality"])
		})
	}
}

func TestGeminiForwardAsOpenAIImagesRejectsInvalidOutputOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiImagesHTTPUpstreamStub{}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	invalidQuality := 101
	tests := []OpenAIImagesRequest{
		{OutputFormat: "bmp"},
		{OutputFormat: "jpeg", OutputCompression: &invalidQuality},
	}
	for _, request := range tests {
		t.Run(request.OutputFormat, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
			request.Endpoint = openAIImagesGenerationsEndpoint
			request.Model = "Nano-Banana-Pro"
			request.Prompt = "draw"
			request.N = 1
			result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), &OpenAIImagesRequest{
				Endpoint: request.Endpoint, Model: request.Model, Prompt: request.Prompt, N: request.N,
				OutputFormat: request.OutputFormat, OutputCompression: request.OutputCompression,
			}, "")
			require.Nil(t, result)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
	require.Zero(t, upstream.calls)
}

func TestGeminiForwardAsOpenAIImagesValidatesRequestedResponseMIME(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name         string
		responseMIME string
		wantError    bool
	}{
		{name: "matching jpeg", responseMIME: "image/jpeg"},
		{name: "mismatching png", responseMIME: "image/png", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &geminiImagesHTTPUpstreamStub{responseMIME: []string{tt.responseMIME}}
			svc := &GeminiMessagesCompatService{
				tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
				httpUpstream:  upstream, cfg: &config.Config{},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
			result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				Model:    "Nano-Banana-Pro", Prompt: "draw", N: 1, OutputFormat: "jpeg",
			}, "")
			if tt.wantError {
				require.Nil(t, result)
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				require.Contains(t, string(failoverErr.ResponseBody), "requested image/jpeg")
				require.Contains(t, failoverErr.ClientMessage, "requested image/jpeg")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, recorder.Body.String(), "aW1hZ2U=")
		})
	}
}

func TestGeminiForwardAsOpenAIImagesMapsOfficialSafetyBlocksToContentPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "prompt feedback",
			body: `{"promptFeedback":{"blockReason":"SAFETY","blockReasonMessage":"The prompt was blocked."}}`,
		},
		{
			name: "candidate finish reason",
			body: `{"candidates":[{"finishReason":"PROHIBITED_CONTENT","finishMessage":"The generated image was blocked.","content":{"parts":[]}}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &geminiImagesHTTPUpstreamStub{responseBodies: []string{tt.body}}
			svc := &GeminiMessagesCompatService{
				tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
				httpUpstream:  upstream, cfg: &config.Config{},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
			result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				Model:    "Nano-Banana-Pro", Prompt: "draw", N: 1,
			}, "")
			require.Nil(t, result)
			var upstreamErr *OpenAIImagesUpstreamError
			require.ErrorAs(t, err, &upstreamErr)
			require.Equal(t, http.StatusBadRequest, upstreamErr.StatusCode)
			require.Equal(t, "content_policy_violation", upstreamErr.Code)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "content_policy_violation", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
			require.Equal(t, 1, upstream.calls, "content-policy responses must not fail over to another account")
		})
	}
}

func TestGeminiOpenAIImagesBillingModelHonorsChannelSource(t *testing.T) {
	result := &OpenAIForwardResult{
		Model:         "Nano-Banana-Pro",
		UpstreamModel: "gemini-3-pro-image",
		BillingModel:  "gemini-3-pro-image",
	}
	tests := []struct {
		name   string
		fields ChannelUsageFields
		want   string
	}{
		{
			name:   "upstream",
			fields: ChannelUsageFields{OriginalModel: "Nano-Banana-Pro", BillingModelSource: BillingModelSourceUpstream},
			want:   "gemini-3-pro-image",
		},
		{
			name:   "requested",
			fields: ChannelUsageFields{OriginalModel: "Nano-Banana-Pro", BillingModelSource: BillingModelSourceRequested},
			want:   "Nano-Banana-Pro",
		},
		{
			name: "channel mapped",
			fields: ChannelUsageFields{
				OriginalModel: "Nano-Banana-Pro", ChannelMappedModel: "Nano-Banana-Pro-priced",
				BillingModelSource: BillingModelSourceChannelMapped,
			},
			want: "Nano-Banana-Pro-priced",
		},
		{
			name: "channel identity mapping keeps upstream",
			fields: ChannelUsageFields{
				OriginalModel: "Nano-Banana-Pro", ChannelMappedModel: "Nano-Banana-Pro",
				BillingModelSource: BillingModelSourceChannelMapped,
			},
			want: "gemini-3-pro-image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resolveOpenAIRecordUsageBillingModel(result, tt.fields))
		})
	}
}

func TestBuildGeminiOpenAIImagesGenerateBodyForcesFlashLiteTo1K(t *testing.T) {
	body, imageSize, err := buildGeminiOpenAIImagesGenerateBody("draw", "3840x2160", "gemini-3.1-flash-lite-image")
	require.NoError(t, err)
	require.Equal(t, ImageBillingSize1K, imageSize)
	require.Equal(t, "1K", stringValueFromJSON(t, body, "generationConfig", "imageConfig", "imageSize"))
	require.Equal(t, "16:9", stringValueFromJSON(t, body, "generationConfig", "imageConfig", "aspectRatio"))
}

func TestBuildGeminiOpenAIImagesGenerateBodyDefaultsAutoTo1K(t *testing.T) {
	for _, size := range []string{"", "auto", " AUTO "} {
		body, imageSize, err := buildGeminiOpenAIImagesGenerateBody("draw", size, "gemini-3.1-flash-image")
		require.NoError(t, err)
		require.Equal(t, ImageBillingSize1K, imageSize)
		require.Equal(t, "1K", stringValueFromJSON(t, body, "generationConfig", "imageConfig", "imageSize"))
	}
}

func TestGeminiOpenAIImagesRequestedSizeUsesPresetPixelBudgets(t *testing.T) {
	tests := []struct {
		size string
		want string
	}{
		{size: "1024x1024", want: ImageBillingSize1K},
		{size: "1024x1536", want: ImageBillingSize1K},
		{size: "1536x1024", want: ImageBillingSize1K},
		{size: "1280x720", want: ImageBillingSize1K},
		{size: "2048x2048", want: ImageBillingSize2K},
		{size: "2560x1440", want: ImageBillingSize2K},
		{size: "1440x2560", want: ImageBillingSize2K},
		{size: "3840x2160", want: ImageBillingSize4K},
		{size: "2160x3840", want: ImageBillingSize4K},
		{size: "4K", want: ImageBillingSize4K},
		{size: "invalid", want: ImageBillingSize1K},
	}
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			require.Equal(t, tt.want, geminiOpenAIImagesRequestedSize(tt.size))
		})
	}
}

func TestBuildGeminiOpenAIImagesRequestBodyIncludesReferences(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nreference")
	dataURL := "data:image/png;base64," + "iVBORw0KGgpyZWZlcmVuY2U="
	parsed := &OpenAIImagesRequest{
		Prompt:         "edit these references",
		Size:           "1536x1024",
		InputImageURLs: []string{dataURL, "https://example.com/style.webp"},
		Uploads: []OpenAIImagesUpload{{
			FileName: "source.png", ContentType: "image/png", Data: png,
		}},
	}

	body, imageSize, err := buildGeminiOpenAIImagesRequestBody(parsed, "gemini-3.1-flash-image")
	require.NoError(t, err)
	require.Equal(t, ImageBillingSize1K, imageSize)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	contents := payload["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 4)
	require.Equal(t, "edit these references", parts[0].(map[string]any)["text"])
	require.Equal(t, "image/png", parts[1].(map[string]any)["inlineData"].(map[string]any)["mimeType"])
	require.Equal(t, "https://example.com/style.webp", parts[2].(map[string]any)["fileData"].(map[string]any)["fileUri"])
	require.Equal(t, "image/webp", parts[2].(map[string]any)["fileData"].(map[string]any)["mimeType"])
	require.Equal(t, "image/png", parts[3].(map[string]any)["inlineData"].(map[string]any)["mimeType"])
}

func TestGeminiForwardAsOpenAIImagesSupportsMultipartEdits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	require.NoError(t, writer.WriteField("model", "Nano-Banana-Pro"))
	require.NoError(t, writer.WriteField("prompt", "replace the background"))
	require.NoError(t, writer.WriteField("size", "1024x1024"))
	png := []byte("\x89PNG\r\n\x1a\nreference")
	for _, field := range []string{"image", "image[1]"} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+field+`.png"`)
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = part.Write(png)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(requestBody.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, requestBody.Bytes())
	require.NoError(t, err)
	require.True(t, parsed.Multipart)
	require.True(t, parsed.IsEdits())
	require.Len(t, parsed.Uploads, 2)
	require.Nil(t, parsed.MaskUpload)

	upstream := &geminiImagesHTTPUpstreamStub{}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	account := geminiImagesServiceAccount()
	result, err := svc.ForwardAsOpenAIImages(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, result.ImageCount)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(upstream.bodies[0], &payload))
	contents := payload["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 3)
	require.Equal(t, "replace the background", parts[0].(map[string]any)["text"])
	require.Equal(t, "image/png", parts[1].(map[string]any)["inlineData"].(map[string]any)["mimeType"])
	require.Equal(t, "image/png", parts[2].(map[string]any)["inlineData"].(map[string]any)["mimeType"])
}

func TestGeminiForwardAsOpenAIImagesRejectsMaskEditing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiImagesHTTPUpstreamStub{}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, nil)
	result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint,
		Model:    "Nano-Banana-Pro", Prompt: "edit", N: 1,
		Uploads: []OpenAIImagesUpload{{FileName: "source.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nsource")}},
		HasMask: true,
		MaskUpload: &OpenAIImagesUpload{
			FileName: "mask.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nmask"),
		},
	}, "")
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "mask editing is not supported")
	require.Zero(t, upstream.calls)
}

func TestGeminiForwardAsOpenAIImagesUsesMappingAndAggregatesN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiImagesHTTPUpstreamStub{}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	account := &Account{
		ID:          615,
		Name:        "vertex-images",
		Platform:    PlatformGemini,
		Type:        AccountTypeServiceAccount,
		Concurrency: 2,
		Credentials: map[string]any{
			"service_account_json": map[string]any{
				"type": "service_account", "project_id": "vertex-project", "private_key_id": "kid",
				"private_key": "unused-in-cache-backed-test", "client_email": "svc@vertex-project.iam.gserviceaccount.com",
			},
			"location":      "global",
			"model_mapping": map[string]any{"Nano-Banana-Pro": "gemini-3-pro-image"},
		},
	}
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    "Nano-Banana-Pro", Prompt: "draw a panorama", N: 2,
		Size: "3840x1648", SizeTier: ImageBillingSize4K, ResponseFormat: "b64_json",
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)

	result, err := svc.ForwardAsOpenAIImages(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 2, upstream.calls)
	require.Equal(t, "Bearer vertex-token", upstream.request.Header.Get("Authorization"))
	require.Contains(t, upstream.request.URL.String(), "/locations/global/publishers/google/models/gemini-3-pro-image:generateContent")
	require.Equal(t, "Nano-Banana-Pro", result.Model)
	require.Equal(t, "gemini-3-pro-image", result.UpstreamModel)
	require.Equal(t, "gemini-3-pro-image", result.BillingModel)
	require.Equal(t, 2, result.ImageCount)
	require.Equal(t, ImageBillingSize4K, result.ImageSize)
	require.Equal(t, []string{ImageBillingSize4K, ImageBillingSize4K}, result.ImageOutputSizes)
	require.Equal(t, 14, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, 2580, result.Usage.ImageOutputTokens)

	var response struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data, 2)
	require.Equal(t, "aW1hZ2U=", response.Data[0].B64JSON)
	require.Equal(t, "refined prompt", response.Data[0].RevisedPrompt)
}

func TestGeminiForwardAsOpenAIImagesReturnsAndBillsPartialNWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiImagesHTTPUpstreamStub{responseStatus: []int{http.StatusOK, http.StatusUnauthorized}}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{tokenCache: &fixedGeminiTokenCache{token: "vertex-token"}},
		httpUpstream:  upstream,
		cfg:           &config.Config{},
	}
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    "Nano-Banana-Pro", Prompt: "draw", N: 2, Size: "auto",
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)

	result, err := svc.ForwardAsOpenAIImages(context.Background(), c, geminiImagesServiceAccount(), parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, []string{ImageBillingSize1K}, result.ImageOutputSizes)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 1290, result.Usage.ImageOutputTokens)
	require.Equal(t, 2, upstream.calls)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data, 1, "only completed images are returned and billed")
}

func geminiImagesServiceAccount() *Account {
	return &Account{
		ID:          615,
		Name:        "vertex-images",
		Platform:    PlatformGemini,
		Type:        AccountTypeServiceAccount,
		Concurrency: 2,
		Credentials: map[string]any{
			"service_account_json": map[string]any{
				"type": "service_account", "project_id": "vertex-project", "private_key_id": "kid",
				"private_key": "unused-in-cache-backed-test", "client_email": "svc@vertex-project.iam.gserviceaccount.com",
			},
			"location":      "global",
			"model_mapping": map[string]any{"Nano-Banana-Pro": "gemini-3-pro-image"},
		},
	}
}

func stringValueFromJSON(t *testing.T, body []byte, keys ...string) string {
	t.Helper()
	var current any
	require.NoError(t, json.Unmarshal(body, &current))
	for _, key := range keys {
		current = current.(map[string]any)[key]
	}
	value, ok := current.(string)
	require.True(t, ok)
	return value
}
