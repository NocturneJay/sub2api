package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 每个解析渠道映射的网关入口都必须把映射后的模型名作为选号别名挂进 ctx，
// 否则该入口会退回上游那套「只改 body、选号仍用原始名」的行为，表现为
// 「渠道映射配好了但请求 404 model_not_found、根本没到上游」。
//
// 这条断言是反向的：新增一个 ResolveChannelMappingAndRestrict 调用点而忘了配
// WithChannelRoutingAlias，本用例就会红并直接点名是哪个文件。
//
// 例外只有 WebSocket 的逐轮重解析（openai_gateway_handler.go 里 turn > 1 的两处）：
// 那两处早就自己做了「原始名或映射名都算支持」的判定，不需要再走 ctx 别名。
func TestEveryChannelMappingEntryPointSeedsRoutingAlias(t *testing.T) {
	const wsPerTurnExemptions = 2

	resolveRe := regexp.MustCompile(`ResolveChannelMappingAndRestrict\(`)
	seedRe := regexp.MustCompile(`WithChannelRoutingAlias\(`)

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	totalResolve, totalSeed := 0, 0
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		require.NoError(t, err)

		resolves := len(resolveRe.FindAll(src, -1))
		if resolves == 0 {
			continue
		}
		seeds := len(seedRe.FindAll(src, -1))
		totalResolve += resolves
		totalSeed += seeds
		if seeds == 0 {
			offenders = append(offenders, name)
		}
	}

	require.Empty(t, offenders,
		"这些文件解析了渠道映射却没有挂选号别名，渠道映射在这些入口上等于没生效：%v", offenders)
	require.Greater(t, totalSeed, 0, "一个别名挂载点都没有，说明这处自研改动被同步上游时整体覆盖掉了")
	require.Equal(t, totalResolve-wsPerTurnExemptions, totalSeed,
		"渠道映射解析点(%d) 与选号别名挂载点(%d) 数量对不上（已扣除 %d 处 WS 逐轮例外）。"+
			"新增入口请一并挂别名；确实不需要的请在本用例里说明理由后调整例外数。",
		totalResolve, totalSeed, wsPerTurnExemptions)
}
