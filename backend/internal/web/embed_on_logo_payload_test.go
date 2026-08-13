//go:build embed

package web

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxInjectedIndexHTMLBytes is the size ceiling for the injected index.html.
// The regression this guards: site_logo stored as a ~122KB base64 data URI is
// inlined twice (favicon + window.__APP_CONFIG__), pushing the document to ~377KB.
const maxInjectedIndexHTMLBytes = 64 * 1024

var faviconHrefRe = regexp.MustCompile(`<link rel="icon"[^>]*href="([^"]*)"`)

func TestInjectedIndexHTMLStaysSmallWithPathLogo(t *testing.T) {
	const logoPath = "/brand/cat-magic-logo.webp"

	server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]any{}})
	require.NoError(t, err)

	settingsJSON, err := json.Marshal(map[string]any{
		"site_name": "猫咪魔法",
		"site_logo": logoPath,
	})
	require.NoError(t, err)

	rendered := string(server.injectSettings(settingsJSON))

	// 1. favicon <link> points at the short path, not a data URI.
	m := faviconHrefRe.FindStringSubmatch(rendered)
	require.Len(t, m, 2, "favicon <link rel=\"icon\"> must exist in the rendered HTML")
	assert.Equal(t, logoPath, m[1], "favicon href must be the configured short path")

	// 2. window.__APP_CONFIG__ carries the same short path.
	assert.Contains(t, rendered, `"site_logo":"`+logoPath+`"`)

	// 3. No base64 image survives anywhere in the document.
	assert.NotContains(t, rendered, "data:image/", "no inline base64 image may remain in index.html")

	// 4. Total document size stays far below the pre-fix ~377KB.
	assert.Less(t, len(rendered), maxInjectedIndexHTMLBytes,
		"injected index.html grew to %d bytes (limit %d)", len(rendered), maxInjectedIndexHTMLBytes)
}

func TestInjectedIndexHTMLBloatsWithBase64Logo(t *testing.T) {
	// Characterises the current production state so the guard above is meaningful:
	// a base64 site_logo really is duplicated into both sinks.
	base64Logo := "data:image/jpeg;base64," + strings.Repeat("A", 120*1024)

	server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]any{}})
	require.NoError(t, err)

	settingsJSON, err := json.Marshal(map[string]any{"site_logo": base64Logo})
	require.NoError(t, err)

	rendered := string(server.injectSettings(settingsJSON))

	assert.Equal(t, 2, strings.Count(rendered, "data:image/jpeg;base64,"),
		"base64 logo is expected to appear twice (favicon + __APP_CONFIG__)")
	assert.Greater(t, len(rendered), 240*1024)
}

func TestSafeImageURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"brand_asset_path", "/brand/cat-magic-logo.webp", "/brand/cat-magic-logo.webp"},
		{"https_absolute", "https://cdn.example.com/logo.webp", "https://cdn.example.com/logo.webp"},
		{"http_absolute_allowed_by_backend", "http://cdn.example.com/logo.webp", "http://cdn.example.com/logo.webp"},
		{"data_image", "data:image/png;base64,abc", "data:image/png;base64,abc"},
		{"protocol_relative_rejected", "//evil.example.com/logo.png", ""},
		{"javascript_rejected", "javascript:alert(1)", ""},
		{"empty_rejected", "   ", ""},
		{"scheme_without_host_rejected", "https:///logo.png", ""},
		{"backslash_escape_is_NOT_rejected", `/\evil.example.com/logo.png`, `/\evil.example.com/logo.png`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, safeImageURL(tc.input))
		})
	}
}
