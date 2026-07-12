//go:build unit

package admin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRedeemPurchaseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty disables button", raw: "   ", want: ""},
		{name: "absolute https", raw: "  https://shop.example.com/redeem?source=app  ", want: "https://shop.example.com/redeem?source=app"},
		{name: "reject http", raw: "http://shop.example.com/redeem", wantErr: true},
		{name: "reject javascript", raw: "javascript:alert(1)", wantErr: true},
		{name: "reject data", raw: "data:text/html,hello", wantErr: true},
		{name: "reject missing host", raw: "https:///redeem", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateRedeemPurchaseURL(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
