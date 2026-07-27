//go:build unit

package repository

import (
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// opsInsertErrorLogPlaceholderCount 解析 insertOpsErrorLogSQL 的 $N 占位符，
// 校验其为 $1..$N 无空缺、无重复，并返回 N。
func opsInsertErrorLogPlaceholderCount(t *testing.T) int {
	t.Helper()
	matches := regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(insertOpsErrorLogSQL, -1)
	require.NotEmpty(t, matches)
	highest := 0
	seen := make(map[int]bool, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		require.False(t, seen[n], "duplicate placeholder $%d", n)
		seen[n] = true
		if n > highest {
			highest = n
		}
	}
	require.Equal(t, highest, len(matches), "placeholders must be $1..$N with no gaps")
	return highest
}

// 列数、占位符数、参数个数三者必须一致。扩列（38→41）时若只改 SQL 与参数构造、
// 漏改写死的期望值，插入会在运行时报 bind message supplies N parameters。
// 这里把断言锚定到语句本身，避免再次静默漂移。
func TestOpsInsertErrorLogArgsMatchStatementShape(t *testing.T) {
	placeholders := opsInsertErrorLogPlaceholderCount(t)

	openIdx := strings.Index(insertOpsErrorLogSQL, "(")
	closeIdx := strings.Index(insertOpsErrorLogSQL, ") VALUES")
	require.Greater(t, closeIdx, openIdx, "unexpected INSERT statement layout")
	columns := 0
	for _, name := range strings.Split(insertOpsErrorLogSQL[openIdx+1:closeIdx], ",") {
		if strings.TrimSpace(name) != "" {
			columns++
		}
	}
	require.Equal(t, placeholders, columns, "column count must match the placeholder count")
	require.Len(t, opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{}), placeholders,
		"arg count must match the placeholder count")
}

func TestOpsInsertErrorLogArgsPreservesExplicitZeroUpstreamStatus(t *testing.T) {
	zero := 0
	args := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{UpstreamStatusCode: &zero})

	require.Len(t, args, opsInsertErrorLogPlaceholderCount(t))
	encoded, ok := args[27].(sql.NullInt64)
	require.True(t, ok)
	require.True(t, encoded.Valid)
	require.Zero(t, encoded.Int64)
}

func TestOpsNullableIntPointerDistinguishesNilZeroAndStatus(t *testing.T) {
	missing := opsNullableIntPointer(nil).(sql.NullInt64)
	require.False(t, missing.Valid)

	zeroValue := 0
	zero := opsNullableIntPointer(&zeroValue).(sql.NullInt64)
	require.True(t, zero.Valid)
	require.Zero(t, zero.Int64)

	statusValue := 503
	status := opsNullableIntPointer(&statusValue).(sql.NullInt64)
	require.True(t, status.Valid)
	require.EqualValues(t, 503, status.Int64)
}
