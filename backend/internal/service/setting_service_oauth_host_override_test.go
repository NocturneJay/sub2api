//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type settingOAuthHostRepoStub struct {
	values map[string]string
}

func (s *settingOAuthHostRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingOAuthHostRepoStub) GetValue(context.Context, string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *settingOAuthHostRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *settingOAuthHostRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingOAuthHostRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingOAuthHostRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingOAuthHostRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func oauthHostTestService(values map[string]string) *SettingService {
	return NewSettingService(&settingOAuthHostRepoStub{values: values}, &config.Config{
		GoogleOAuth: config.EmailOAuthProviderConfig{
			Enabled:             true,
			ClientID:            "google-primary-id",
			ClientSecret:        "google-primary-secret",
			RedirectURL:         "https://api.aicatstudios.com/api/v1/auth/oauth/google/callback",
			FrontendRedirectURL: "/auth/oauth/callback",
		},
		LinuxDo: config.LinuxDoConnectConfig{
			Enabled:             true,
			ClientID:            "linuxdo-primary-id",
			ClientSecret:        "linuxdo-primary-secret",
			AuthorizeURL:        "https://connect.linux.do/oauth2/authorize",
			TokenURL:            "https://connect.linux.do/oauth2/token",
			UserInfoURL:         "https://connect.linux.do/api/user",
			Scopes:              "openid profile email",
			RedirectURL:         "https://api.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback",
			FrontendRedirectURL: "/auth/linuxdo/callback",
			TokenAuthMethod:     "client_secret_post",
		},
	})
}

func TestSettingService_OAuthConfigForHostKeepsPrimaryConfig(t *testing.T) {
	svc := oauthHostTestService(map[string]string{
		SettingKeyGoogleOAuthAPICnRedirectURL:     "https://api-cn.aicatstudios.com/api/v1/auth/oauth/google/callback",
		SettingKeyLinuxDoConnectAPICnClientID:     "linuxdo-api-cn-id",
		SettingKeyLinuxDoConnectAPICnClientSecret: "linuxdo-api-cn-secret",
		SettingKeyLinuxDoConnectAPICnRedirectURL:  "https://api-cn.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback",
	})

	googleCfg, err := svc.GetEmailOAuthProviderConfigForHost(context.Background(), "google", "api.aicatstudios.com")
	require.NoError(t, err)
	require.Equal(t, "google-primary-id", googleCfg.ClientID)
	require.Equal(t, "https://api.aicatstudios.com/api/v1/auth/oauth/google/callback", googleCfg.RedirectURL)

	linuxDoCfg, err := svc.GetLinuxDoConnectOAuthConfigForHost(context.Background(), "api.aicatstudios.com")
	require.NoError(t, err)
	require.Equal(t, "linuxdo-primary-id", linuxDoCfg.ClientID)
	require.Equal(t, "https://api.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback", linuxDoCfg.RedirectURL)
}

func TestSettingService_OAuthConfigForHostSelectsAPICnOverrides(t *testing.T) {
	svc := oauthHostTestService(map[string]string{
		SettingKeyGoogleOAuthAPICnRedirectURL:     "https://api-cn.aicatstudios.com/api/v1/auth/oauth/google/callback",
		SettingKeyLinuxDoConnectAPICnClientID:     "linuxdo-api-cn-id",
		SettingKeyLinuxDoConnectAPICnClientSecret: "linuxdo-api-cn-secret",
		SettingKeyLinuxDoConnectAPICnRedirectURL:  "https://api-cn.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback",
	})

	googleCfg, err := svc.GetEmailOAuthProviderConfigForHost(context.Background(), "google", "API-CN.AICATSTUDIOS.COM:443")
	require.NoError(t, err)
	require.Equal(t, "google-primary-id", googleCfg.ClientID)
	require.Equal(t, "https://api-cn.aicatstudios.com/api/v1/auth/oauth/google/callback", googleCfg.RedirectURL)

	linuxDoCfg, err := svc.GetLinuxDoConnectOAuthConfigForHost(context.Background(), "api-cn.aicatstudios.com:443")
	require.NoError(t, err)
	require.Equal(t, "linuxdo-api-cn-id", linuxDoCfg.ClientID)
	require.Equal(t, "linuxdo-api-cn-secret", linuxDoCfg.ClientSecret)
	require.Equal(t, "https://api-cn.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback", linuxDoCfg.RedirectURL)
}

func TestSettingService_OAuthConfigForHostRejectsIncompleteOrWrongHostOverrides(t *testing.T) {
	svc := oauthHostTestService(map[string]string{
		SettingKeyGoogleOAuthAPICnRedirectURL:    "https://evil.example/api/v1/auth/oauth/google/callback",
		SettingKeyLinuxDoConnectAPICnClientID:    "linuxdo-api-cn-id",
		SettingKeyLinuxDoConnectAPICnRedirectURL: "https://api-cn.aicatstudios.com/api/v1/auth/oauth/linuxdo/callback",
	})

	_, err := svc.GetEmailOAuthProviderConfigForHost(context.Background(), "google", "api-cn.aicatstudios.com")
	require.Error(t, err)

	_, err = svc.GetLinuxDoConnectOAuthConfigForHost(context.Background(), "api-cn.aicatstudios.com")
	require.Error(t, err)

	googleCfg, err := svc.GetEmailOAuthProviderConfigForHost(context.Background(), "google", "attacker.example")
	require.NoError(t, err)
	require.Equal(t, "https://api.aicatstudios.com/api/v1/auth/oauth/google/callback", googleCfg.RedirectURL)

	googleCfg, err = svc.GetEmailOAuthProviderConfigForHost(context.Background(), "google", "api-cn.aicatstudios.com:443.evil.example")
	require.NoError(t, err)
	require.Equal(t, "https://api.aicatstudios.com/api/v1/auth/oauth/google/callback", googleCfg.RedirectURL)
}
