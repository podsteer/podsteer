package assets

import (
	"io/fs"
	"strings"
	"testing"
)

// TestTheShippedPolicyPermitsNoOutboundConnections guards a product
// commitment, not a style rule.
//
// The community build must never contact anything PodSteer operates, and
// CLAUDE.md names two things that keep that honest: no HTTP client outside
// adapters/k8s, and this policy. `connect-src 'self' ws: wss:` does not keep
// it — a bare scheme source is a wildcard, so those two tokens permit a
// WebSocket to any host on the internet from inside the webview. They are
// there for Vite's hot reload, they were shipping, and nothing noticed
// because the page never tries.
func TestTheShippedPolicyPermitsNoOutboundConnections(t *testing.T) {
	page, err := fs.ReadFile(dist, "dist/index.html")
	if err != nil {
		t.Skip("no frontend bundle embedded; build it with `npm --prefix web run build`")
	}

	policy := string(page)
	if !strings.Contains(policy, "Content-Security-Policy") {
		t.Fatal("the shipped page declares no content security policy at all")
	}
	for _, wildcard := range []string{" ws:", " wss:", " http:", " https:", " *"} {
		if strings.Contains(policy, "connect-src") && strings.Contains(policy, wildcard) {
			t.Errorf("the shipped policy carries the bare scheme source %q, which permits any host", strings.TrimSpace(wildcard))
		}
	}
	if !strings.Contains(policy, "connect-src 'self';") {
		t.Error("the shipped policy does not restrict connect-src to 'self'")
	}
}
