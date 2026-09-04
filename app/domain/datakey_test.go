package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestValidDataKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"empty", "", false},
		{"simple", "password", true},
		{"dots", "tls.crt", true},
		{"underscores and dashes", "DATABASE_URL-primary", true},
		{"digits", "key123", true},
		{"leading dot", ".dockerconfigjson", true},
		{"slash", "a/b", false},
		{"space", "a b", false},
		{"equals", "a=b", false},
		{"unicode", "clé", false},
		{"newline", "a\nb", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := domain.ValidDataKey(tt.key); got != tt.want {
				t.Errorf("ValidDataKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
