//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestUsageRecordContext_PropagatesChannelMonitorProbe 锁住渠道监控探测标记跨越
// 异步记账边界的传播。
//
// 用量记录任务跑在 worker 池的全新 background context 上（见
// usage_record_worker_pool.go 里的 context.WithTimeout(context.Background(), ...)），
// 请求 context 里的值一律不会自动带过去；usageRecordContext 正是为此存在，
// 逐个搬运需要跨界的请求级标量。
//
// 该功能上线首版就漏了这一步：中间件正确标记了请求 context，但用量行落库时读到的
// 是空白的 background context，于是标记恒为 false —— 监控照常跑、记录照常写，
// 就是一条都标不上，没有任何报错。本用例防止再次退化。
func TestUsageRecordContext_PropagatesChannelMonitorProbe(t *testing.T) {
	parent := service.WithChannelMonitorProbe(context.Background())
	// base 模拟 worker 池那个与请求无关的全新 context。
	base := context.Background()

	got := usageRecordContext(parent, base)

	require.True(t, service.IsChannelMonitorProbe(got),
		"探测标记必须跨过异步记账边界，否则用量行永远标不上")
}

func TestUsageRecordContext_DoesNotInventChannelMonitorProbe(t *testing.T) {
	got := usageRecordContext(context.Background(), context.Background())
	require.False(t, service.IsChannelMonitorProbe(got),
		"未标记的请求不得被凭空标记")

	// parent 为 nil 时直接返回 base，同样不得带上标记。
	require.False(t, service.IsChannelMonitorProbe(usageRecordContext(nil, context.Background())))
}

// TestEnqueueOpsErrorLogStampsChannelMonitorFromContext 锁住错误日志的标记接线。
//
// 与用量行同一道理：错误日志也走队列 + worker，worker 跑在与请求无关的 context 上。
// 所以标记必须在入队时（仍在请求 goroutine 内）从 ctx 读出、写进 entry 载荷，
// 而不是留在 context 里等 worker 去读——那样永远读不到。
// enqueueOpsErrorLog 是唯一入队口，在那里盖章即可覆盖全部 5 个调用点。
//
// 这里真调 enqueueOpsErrorLog（而不是复刻它的逻辑），否则测的只是测试自己。
// 用容量足够的测试队列接住 entry，入队后直接检查载荷上的标记。
func TestEnqueueOpsErrorLogStampsChannelMonitorFromContext(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)
	opsErrorLogOnce.Do(func() {})

	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, 4)
	opsErrorLogMu.Unlock()

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	probe := &service.OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}
	enqueueOpsErrorLog(service.WithChannelMonitorProbe(context.Background()), ops, probe)
	require.True(t, probe.IsChannelMonitor, "探测请求必须在入队时被盖章")

	plain := &service.OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}
	enqueueOpsErrorLog(context.Background(), ops, plain)
	require.False(t, plain.IsChannelMonitor, "普通请求不得被凭空盖章")
}
