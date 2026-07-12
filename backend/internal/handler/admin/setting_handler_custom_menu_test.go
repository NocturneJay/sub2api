package admin

import "testing"

func TestNormalizeCustomMenuOpenMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "legacy empty defaults to iframe", input: "", want: "iframe"},
		{name: "whitespace defaults to iframe", input: "  ", want: "iframe"},
		{name: "iframe", input: "iframe", want: "iframe"},
		{name: "new tab", input: "new_tab", want: "new_tab"},
		{name: "trimmed new tab", input: " new_tab ", want: "new_tab"},
		{name: "reject unknown", input: "same_tab", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeCustomMenuOpenMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeCustomMenuOpenMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeCustomMenuOpenMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
