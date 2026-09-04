package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestValidImageReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"empty", "", false},
		{"bare name", "nginx", true},
		{"bare name with tag", "nginx:1.25", true},
		{"two path segments, no registry", "library/nginx", true},
		{"registry with path and tag", "registry.example.com/team/app:v1.2.3", true},
		{"registry with port, path and tag", "myregistry.io:5000/team/app:v1.2.3", true},
		{"registry with port, no tag", "myregistry.io:5000/team/app", true},
		{"localhost registry", "localhost/app:dev", true},
		{"localhost registry with port", "localhost:5000/app", true},
		{"digest only", "app@sha256:" + hex64, true},
		{"registry, path and digest", "registry.example.com/team/app@sha256:" + hex64, true},
		{"tag and digest together", "app:v1@sha256:" + hex64, true},
		{"underscored component", "my_app", true},
		{"dashed component", "my-app", true},
		{"dotted component", "my.app", true},

		{"whitespace inside", "ngi nx", false},
		{"leading space", " nginx", false},
		{"trailing newline", "nginx\n", false},
		{"empty tag", "nginx:", false},
		{"tag starting with a dot", "nginx:.bad", false},
		{"empty digest", "app@", false},
		{"malformed digest algorithm case", "app@SHA256:" + hex64, false},
		{"digest too short", "app@sha256:abc123", false},
		{"digest not hex", "app@sha256:" + "z" + hex64[1:], false},
		{"empty path segment", "team//app", false},
		{"leading slash", "/nginx", false},
		{"trailing slash", "nginx/", false},
		{"uppercase component", "Nginx", false},
		{"uppercase in path", "team/App", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := domain.ValidImageReference(tt.ref); got != tt.want {
				t.Errorf("ValidImageReference(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

// hex64 is a syntactically valid sha256 digest body — 64 lowercase hex
// characters — used to build test references without repeating it inline.
const hex64 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"
