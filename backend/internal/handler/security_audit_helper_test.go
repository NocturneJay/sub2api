package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCachesSecurityAuditCompletionSkipsWebSocketStages(t *testing.T) {
	require.True(t, cachesSecurityAuditCompletion("http"))
	require.True(t, cachesSecurityAuditCompletion(""))
	require.False(t, cachesSecurityAuditCompletion("first_turn"))
	require.False(t, cachesSecurityAuditCompletion("subsequent_turn"))
}

func TestRunSecurityAuditCapturesCyberPolicyEvidenceAndStableRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext := context.WithValue(context.Background(), ctxkey.RequestID, "request-real-123")
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
	c.Header("X-Request-Id", "00000000000000000000000000000000")

	body := []byte(`{"messages":[{"role":"system","content":"system history"},{"role":"user","content":"latest user request password=supersecret123"}]}`)
	decision := runSecurityAudit(c, nil, nil, nil, nil, middleware2.AuthSubject{UserID: 7},
		"openai_chat_completions", "gpt-test", body, "http")
	require.Nil(t, decision)

	evidence := getCyberPolicyAuditEvidence(c)
	require.Contains(t, evidence.InputExcerpt, "latest user request")
	require.NotContains(t, evidence.InputExcerpt, "system history")
	require.NotContains(t, evidence.InputExcerpt, "supersecret123")
	require.Equal(t, "request-real-123", cyberPolicyRequestID(c))

	clearCyberPolicyTurnState(c)
	require.Empty(t, getCyberPolicyAuditEvidence(c).InputExcerpt)
}

func TestRunSecurityAuditClearsStaleCyberPolicyEvidenceWhenPromptIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(cyberPolicyAuditEvidenceContextKey, cyberPolicyAuditEvidence{InputExcerpt: "stale turn"})

	decision := runSecurityAudit(c, nil, nil, nil, nil, middleware2.AuthSubject{UserID: 7},
		"openai_responses", "gpt-test", []byte(`{"type":"conversation.item.create"}`), "subsequent_turn")
	require.Nil(t, decision)
	require.Empty(t, getCyberPolicyAuditEvidence(c).InputExcerpt)
}

func TestRunSecurityAuditDoesNotSkipSubsequentWebSocketTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := &turnCountingEngine{mode: securityaudit.ModeAsync}
	coordinator := securityaudit.NewCoordinator(nil, engine)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	subject := middleware2.AuthSubject{UserID: 7, Concurrency: 1}
	first := runSecurityAudit(c, nil, coordinator, nil, nil, subject, "openai_responses", "gpt-test",
		[]byte(`{"type":"response.create","response":{"input":"benign"}}`), "first_turn")
	require.NotNil(t, first)
	require.True(t, first.AllowNextStage)
	require.Equal(t, int64(1), engine.enqueues.Load())
	_, cached := c.Get(securityAuditCompletedContextKey)
	require.False(t, cached, "WebSocket stages must not set the HTTP completion cache")

	// Even if an HTTP path previously cached completion on this Context, WS turns
	// must still audit every response.create payload.
	c.Set(securityAuditCompletedContextKey, true)

	second := runSecurityAudit(c, nil, coordinator, nil, nil, subject, "openai_responses", "gpt-test",
		[]byte(`{"type":"response.create","response":{"input":"malicious follow-up"}}`), "subsequent_turn")
	require.NotNil(t, second)
	require.Equal(t, int64(2), engine.enqueues.Load(), "subsequent WebSocket turns must be audited again")
}

type turnCountingEngine struct {
	mode     securityaudit.Mode
	enqueues atomic.Int64
}

func (e *turnCountingEngine) EffectiveMode() securityaudit.Mode { return e.mode }
func (e *turnCountingEngine) Enqueue(context.Context, securityaudit.Request) error {
	e.enqueues.Add(1)
	return nil
}
func (e *turnCountingEngine) Evaluate(context.Context, securityaudit.Request) (*securityaudit.PromptDecision, error) {
	return &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}, nil
}
