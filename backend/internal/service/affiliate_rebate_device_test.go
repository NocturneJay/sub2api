package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type affiliateRebateRepoStub struct {
	inviteeSummary *AffiliateSummary
	inviterSummary *AffiliateSummary
	accrueCalls    []float64
	conflictChecks []string
	conflict       bool
}

func (r *affiliateRebateRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	switch {
	case r.inviteeSummary != nil && r.inviteeSummary.UserID == userID:
		cp := *r.inviteeSummary
		return &cp, nil
	case r.inviterSummary != nil && r.inviterSummary.UserID == userID:
		cp := *r.inviterSummary
		return &cp, nil
	default:
		return &AffiliateSummary{UserID: userID, AffCode: "AFFTEST", CreatedAt: time.Now().Add(-time.Hour)}, nil
	}
}

func (r *affiliateRebateRepoStub) SetSignupDeviceHash(context.Context, int64, string) error {
	panic("unexpected SetSignupDeviceHash call")
}

func (r *affiliateRebateRepoStub) GetAffiliateByCode(context.Context, string) (*AffiliateSummary, error) {
	panic("unexpected GetAffiliateByCode call")
}

func (r *affiliateRebateRepoStub) BindInviter(context.Context, int64, int64) (bool, error) {
	panic("unexpected BindInviter call")
}

func (r *affiliateRebateRepoStub) AccrueQuota(_ context.Context, _ int64, _ int64, amount float64, _ int, _ *int64) (bool, error) {
	r.accrueCalls = append(r.accrueCalls, amount)
	return true, nil
}

func (r *affiliateRebateRepoStub) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	return 0, nil
}

func (r *affiliateRebateRepoStub) HasSignupDeviceRebateConflict(_ context.Context, _ int64, _ int64, deviceHash string) (bool, error) {
	r.conflictChecks = append(r.conflictChecks, deviceHash)
	return r.conflict, nil
}

func (r *affiliateRebateRepoStub) ThawFrozenQuota(context.Context, int64) (float64, error) {
	panic("unexpected ThawFrozenQuota call")
}

func (r *affiliateRebateRepoStub) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

func (r *affiliateRebateRepoStub) ListInvitees(context.Context, int64, int) ([]AffiliateInvitee, error) {
	panic("unexpected ListInvitees call")
}

func (r *affiliateRebateRepoStub) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

func (r *affiliateRebateRepoStub) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

func (r *affiliateRebateRepoStub) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

func (r *affiliateRebateRepoStub) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

func (r *affiliateRebateRepoStub) ListUsersWithCustomSettings(context.Context, AffiliateAdminFilter) ([]AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

func (r *affiliateRebateRepoStub) ListAffiliateInviteRecords(context.Context, AffiliateRecordFilter) ([]AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

func (r *affiliateRebateRepoStub) ListAffiliateRebateRecords(context.Context, AffiliateRecordFilter) ([]AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

func (r *affiliateRebateRepoStub) ListAffiliateTransferRecords(context.Context, AffiliateRecordFilter) ([]AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

func (r *affiliateRebateRepoStub) GetAffiliateUserOverview(context.Context, int64) (*AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

type affiliateRebateSettingRepoStub struct {
	values map[string]string
}

func (s *affiliateRebateSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *affiliateRebateSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (s *affiliateRebateSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *affiliateRebateSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *affiliateRebateSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *affiliateRebateSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *affiliateRebateSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func newAffiliateRebateDeviceTestService(repo *affiliateRebateRepoStub) *AffiliateService {
	settingSvc := NewSettingService(&affiliateRebateSettingRepoStub{values: map[string]string{
		SettingKeyAffiliateEnabled:           "true",
		SettingKeyAffiliateRebateRate:        "20",
		SettingKeyAffiliateRebateFreezeHours: "0",
	}}, nil)
	return NewAffiliateService(repo, settingSvc, nil, nil)
}

func TestAccrueInviteRebateForOrderAllowsMissingSignupDeviceHash(t *testing.T) {
	inviterID := int64(10)
	repo := &affiliateRebateRepoStub{
		inviteeSummary: &AffiliateSummary{
			UserID:    20,
			AffCode:   "INVITEE",
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-time.Hour),
		},
		inviterSummary: &AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		},
	}

	rebate, err := newAffiliateRebateDeviceTestService(repo).AccrueInviteRebateForOrder(context.Background(), 20, 100, nil)

	require.NoError(t, err)
	require.InDelta(t, 20, rebate, 1e-9)
	require.Empty(t, repo.conflictChecks)
	require.Equal(t, []float64{20}, repo.accrueCalls)
}

func TestAccrueInviteRebateForOrderIgnoresSignupDeviceConflict(t *testing.T) {
	inviterID := int64(10)
	deviceHash := "device-hash"
	repo := &affiliateRebateRepoStub{
		inviteeSummary: &AffiliateSummary{
			UserID:           20,
			AffCode:          "INVITEE",
			InviterID:        &inviterID,
			SignupDeviceHash: &deviceHash,
			CreatedAt:        time.Now().Add(-time.Hour),
		},
		inviterSummary: &AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		},
		conflict: true,
	}

	rebate, err := newAffiliateRebateDeviceTestService(repo).AccrueInviteRebateForOrder(context.Background(), 20, 100, nil)

	require.NoError(t, err)
	require.InDelta(t, 20, rebate, 1e-9)
	require.Empty(t, repo.conflictChecks)
	require.Equal(t, []float64{20}, repo.accrueCalls)
}

var _ AffiliateRepository = (*affiliateRebateRepoStub)(nil)
var _ SettingRepository = (*affiliateRebateSettingRepoStub)(nil)
