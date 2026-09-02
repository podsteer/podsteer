package application

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// ReleaseSource reports the newest published release of PodSteer.
//
// An outbound port. The interface exists so the service can be tested against
// a source that fails, is rate limited, or is never called at all — that last
// one being the property that actually matters here.
type ReleaseSource interface {
	// Latest returns the newest release's tag and its page.
	Latest(ctx context.Context) (tag string, url string, err error)
}

// disableEnv turns the check off for everybody on a machine, whatever the
// interface says.
//
// FOR PACKAGERS AND ADMINISTRATORS, and it is not a duplicate of the setting
// in Settings. A distribution that ships PodSteer through its own package
// manager wants the in-app check gone rather than merely defaulted off, and an
// operator running under a deny-by-default egress policy needs it disabled by
// configuration rather than by asking every user to find a checkbox. Headlamp
// and krew both expose exactly this.
const disableEnv = "PODSTEER_UPDATE_CHECK"

// checkInterval is the most often the source is asked.
//
// Once a day. A release happens at most weekly; asking more often buys nothing
// and turns the check into a beacon that reports when the application is
// running.
const checkInterval = 24 * time.Hour

// failureBackoff is how long a failed check is remembered.
//
// FAILURES ARE CACHED, and this is not an optimisation. k9s does not cache
// them, so a machine behind a firewall retries on every refresh — the bug its
// users describe as "it tries hard every other second". A refusal here is
// worth exactly as much as a success for the purpose of not asking again soon.
const failureBackoff = 4 * time.Hour

// UpdateService answers whether a newer PodSteer has been published.
//
// NOTHING HERE RUNS ON ITS OWN. There is no goroutine and no ticker: the
// service acts only when the interface asks it to, and the interface only asks
// when the operator has left the check switched on. That is what makes the off
// switch auditable — off is not a flag consulted deep inside a running loop,
// it is nothing happening.
type UpdateService struct {
	source    ReleaseSource
	installed string
	logger    *slog.Logger

	mu        sync.Mutex
	last      domain.UpdateCheck
	lastAt    time.Time
	haveCheck bool
}

// NewUpdateService returns the service for a given build.
func NewUpdateService(source ReleaseSource, installedVersion string, logger *slog.Logger) *UpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &UpdateService{source: source, installed: installedVersion, logger: logger}
}

// Enabled reports whether checking is permitted on this machine at all.
//
// Read from the environment on every call rather than cached at construction,
// so an administrator's change takes effect on restart without depending on
// where in the boot sequence this happened to be built.
func (s *UpdateService) Enabled() bool {
	raw := strings.TrimSpace(os.Getenv(disableEnv))
	if raw == "" {
		return true
	}

	// Anything unparseable is treated as "off". The variable exists to
	// suppress a network call, and a typo in it must not quietly restore the
	// call it was set to prevent.
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}

// Check returns what is known about newer releases, asking at most once a day.
//
// `force` is a person pressing "Check now", which bypasses the interval but
// not the environment switch: an explicit request is still a request, and an
// administrator who turned this off did not mean "except on request".
func (s *UpdateService) Check(ctx context.Context, force bool) domain.UpdateCheck {
	if !s.Enabled() {
		return domain.UpdateCheck{State: domain.UpdateDisabled, Installed: s.installed}
	}

	s.mu.Lock()
	if s.haveCheck && !force && time.Since(s.lastAt) < s.interval() {
		cached := s.last
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	tag, url, err := s.source.Latest(ctx)
	if err != nil {
		// DEBUG, NOT ERROR, and not surfaced. Being unable to reach GitHub
		// says nothing about the cluster the operator is actually working on,
		// and an alarm about it would be noise on every airgapped machine.
		s.logger.Debug("update check did not complete", slog.String("error", err.Error()))
		return s.remember(domain.UpdateCheck{State: domain.UpdateUnknown, Installed: s.installed})
	}

	result := domain.CompareVersions(s.installed, tag)
	// ONLY WHEN THERE IS SOMETHING TO POINT AT. CompareVersions deliberately
	// returns an empty Latest for a state that has nothing to show — a
	// development build, or an answer that would not parse — and attaching a
	// release URL anyway gives the interface a link it has no basis to offer.
	if result.State == domain.UpdateAvailable || result.State == domain.UpdateCurrent {
		result.URL = url
	}
	return s.remember(result)
}

// interval is how long the last result stands.
func (s *UpdateService) interval() time.Duration {
	if s.haveCheck && s.last.State == domain.UpdateUnknown {
		return failureBackoff
	}
	return checkInterval
}

// remember stores a result and returns it.
func (s *UpdateService) remember(result domain.UpdateCheck) domain.UpdateCheck {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.last = result
	s.lastAt = time.Now()
	s.haveCheck = true
	return result
}
