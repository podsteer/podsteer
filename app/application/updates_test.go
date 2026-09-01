package application_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// countingSource records whether it was asked anything at all.
type countingSource struct {
	calls atomic.Int32
	tag   string
	err   error
}

func (s *countingSource) Latest(context.Context) (string, string, error) {
	s.calls.Add(1)
	if s.err != nil {
		return "", "", s.err
	}
	return s.tag, "https://github.com/podsteer/podsteer/releases/tag/" + s.tag, nil
}

// THE TEST THE RESEARCH SAID EVERYONE GETS WRONG. An opt-out that is shipped
// but never asserted has silently broken in k9s, Terraform, dotnet, JetBrains
// and Docker Desktop — in Terraform's case, open for years. Asserting the
// result is "disabled" is not enough: the question is whether the request was
// made, so this counts calls to the source.
func TestDisablingSuppressesTheRequestEntirely(t *testing.T) {
	for _, value := range []string{"false", "0", "no", "nonsense", "FALSE"} {
		t.Setenv("PODSTEER_UPDATE_CHECK", value)

		source := &countingSource{tag: "v9.9.9"}
		service := application.NewUpdateService(source, "v0.1.1", nil)

		result := service.Check(context.Background(), false)

		if calls := source.calls.Load(); calls != 0 {
			t.Fatalf("%q: the source was asked %d times", value, calls)
		}
		if result.State != domain.UpdateDisabled {
			t.Fatalf("%q: state %q, want disabled", value, result.State)
		}
	}
}

func TestForcingACheckStillRespectsTheEnvironment(t *testing.T) {
	// An administrator who turned this off did not mean "except when somebody
	// presses the button".
	t.Setenv("PODSTEER_UPDATE_CHECK", "false")

	source := &countingSource{tag: "v9.9.9"}
	service := application.NewUpdateService(source, "v0.1.1", nil)

	service.Check(context.Background(), true)

	if calls := source.calls.Load(); calls != 0 {
		t.Fatalf("a forced check made %d requests despite being disabled", calls)
	}
}

func TestAnUnsetVariableLeavesCheckingOn(t *testing.T) {
	t.Setenv("PODSTEER_UPDATE_CHECK", "")

	source := &countingSource{tag: "v0.2.0"}
	service := application.NewUpdateService(source, "v0.1.1", nil)

	if result := service.Check(context.Background(), false); result.State != domain.UpdateAvailable {
		t.Fatalf("state %q, want available", result.State)
	}
}

func TestTheSourceIsAskedOnceAndThenCached(t *testing.T) {
	t.Setenv("PODSTEER_UPDATE_CHECK", "true")

	source := &countingSource{tag: "v0.2.0"}
	service := application.NewUpdateService(source, "v0.1.1", nil)

	for range 5 {
		service.Check(context.Background(), false)
	}

	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("asked %d times, want 1 — the interval is not holding", calls)
	}
}

func TestAFailedCheckIsAlsoCached(t *testing.T) {
	// k9s caches successes and not failures, so a machine behind a firewall
	// retries forever. A refusal is worth as much as an answer for the
	// purpose of not asking again immediately.
	t.Setenv("PODSTEER_UPDATE_CHECK", "true")

	source := &countingSource{err: errors.New("no route to host")}
	service := application.NewUpdateService(source, "v0.1.1", nil)

	for range 5 {
		if result := service.Check(context.Background(), false); result.State != domain.UpdateUnknown {
			t.Fatalf("state %q, want unknown", result.State)
		}
	}

	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("a failing source was asked %d times, want 1", calls)
	}
}

func TestCheckNowBypassesTheInterval(t *testing.T) {
	t.Setenv("PODSTEER_UPDATE_CHECK", "true")

	source := &countingSource{tag: "v0.2.0"}
	service := application.NewUpdateService(source, "v0.1.1", nil)

	service.Check(context.Background(), false)
	service.Check(context.Background(), true)

	if calls := source.calls.Load(); calls != 2 {
		t.Fatalf("asked %d times, want 2", calls)
	}
}

func TestADevelopmentBuildIsNeverToldToUpgrade(t *testing.T) {
	// `config.Version()` is "dev" for anything built from a working tree.
	// Claiming an update is available would be telling somebody mid-change
	// that the code they are editing is out of date.
	t.Setenv("PODSTEER_UPDATE_CHECK", "true")

	source := &countingSource{tag: "v9.9.9"}
	service := application.NewUpdateService(source, "dev", nil)

	result := service.Check(context.Background(), false)
	if result.State != domain.UpdateUnknown {
		t.Fatalf("state %q, want unknown for a development build", result.State)
	}
	// And nothing to click. A link offered beside a state that has nothing to
	// say is a link with no basis.
	if result.Latest != "" || result.URL != "" {
		t.Fatalf("leaked latest=%q url=%q into a development build", result.Latest, result.URL)
	}
}
