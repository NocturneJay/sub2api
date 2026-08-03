//go:build unit

package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestOpsInsertErrorLogPersistsChannelMonitorFlag 钉死 is_channel_monitor 的入库接线。
// 该标记在中间件（请求上下文内）读出后写进 entry 载荷，随 entry 入队；
// 入队后的 worker 跑在与请求无关的 context 上，届时再读永远读不到 ——
// 用量行那边正是因为把标记留在 context 里而静默失效过一整轮。
func TestOpsInsertErrorLogPersistsChannelMonitorFlag(t *testing.T) {
	require.Contains(t, insertOpsErrorLogSQL, "is_channel_monitor",
		"INSERT 列清单必须包含 is_channel_monitor")

	placeholders := opsInsertErrorLogPlaceholderCount(t)

	on := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{IsChannelMonitor: true})
	require.Len(t, on, placeholders)
	flag, ok := on[placeholders-1].(bool)
	require.True(t, ok, "is_channel_monitor 应为 bool，实际 %T", on[placeholders-1])
	require.True(t, flag)

	off := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{})
	flagOff, ok := off[placeholders-1].(bool)
	require.True(t, ok)
	require.False(t, flagOff, "未标记的请求必须写入 false")
}

// TestOpsErrorLogsWhereExcludesChannelMonitor 锁住列表过滤条件。
func TestOpsErrorLogsWhereExcludesChannelMonitor(t *testing.T) {
	on, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{ExcludeChannelMonitor: true})
	require.True(t, strings.Contains(on, "is_channel_monitor"),
		"开启排除时 WHERE 必须带 is_channel_monitor 条件，实际: %s", on)

	off, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{})
	require.False(t, strings.Contains(off, "is_channel_monitor"),
		"未开启时不得引入该条件（默认视图必须包含监控记录）")
}
