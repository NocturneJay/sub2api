//go:build unit

package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newChannelMonitorUsageLog(isMonitor bool) *service.UsageLog {
	return &service.UsageLog{
		UserID:           1,
		APIKeyID:         2,
		AccountID:        3,
		RequestID:        "req-channel-monitor",
		Model:            "gpt-5",
		InputTokens:      10,
		OutputTokens:     5,
		TotalCost:        1.0,
		ActualCost:       1.0,
		IsChannelMonitor: isMonitor,
		CreatedAt:        time.Now().UTC(),
	}
}

// TestPrepareUsageLogInsert_ChannelMonitorArgWiring 把 is_channel_monitor 钉在
// arg 表与 arg 切片的同一个位置上。usageLogInsertArgTypes 的注释要求这几个清单
// 顺序严格一致，加列时任何一处漏改都会让所有列静默错位——那是最难排查的一类故障。
// is_channel_monitor 排在 session_id 之前，即倒数第三个（created_at 最后）。
func TestPrepareUsageLogInsert_ChannelMonitorArgWiring(t *testing.T) {
	require.Len(t, usageLogInsertArgTypes, 58,
		"arg-type table must include is_channel_monitor")
	require.Equal(t, "boolean", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-3],
		"is_channel_monitor arg type must be boolean")

	prepared := prepareUsageLogInsert(newChannelMonitorUsageLog(true))
	require.Len(t, prepared.args, len(usageLogInsertArgTypes),
		"prepared args must match the arg-type table length")

	flag, ok := prepared.args[len(prepared.args)-3].(bool)
	require.True(t, ok, "is_channel_monitor arg should be a bool, got %T",
		prepared.args[len(prepared.args)-3])
	require.True(t, flag)

	preparedFalse := prepareUsageLogInsert(newChannelMonitorUsageLog(false))
	flagFalse, ok := preparedFalse.args[len(preparedFalse.args)-3].(bool)
	require.True(t, ok)
	require.False(t, flagFalse, "未标记的请求必须写入 false")
}

// TestUsageLogQueries_IncludeChannelMonitor 确认每条生成的 INSERT 路径与
// SELECT 列清单都带上了新列。漏掉任意一条都会让该路径写入时列数不匹配而报错，
// 或让读取路径的扫描顺序错位。
func TestUsageLogQueries_IncludeChannelMonitor(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "is_channel_monitor",
		"SELECT column list must include is_channel_monitor")

	log := newChannelMonitorUsageLog(true)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	require.Contains(t, batchQuery, "is_channel_monitor")
	require.GreaterOrEqual(t, strings.Count(batchQuery, "is_channel_monitor"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1)

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, bestEffortQuery, "is_channel_monitor")
	require.Len(t, bestEffortArgs, len(prepared.args))
}
