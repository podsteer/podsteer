package wails

import (
	"context"
	"log/slog"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"podsteer/app/domain"
	"podsteer/app/ports"
)

// DefaultRequestTimeout bounds a single call from the frontend.
//
// It exists so a cluster that accepts a TCP connection and then goes quiet — a
// half-open VPN tunnel, a wedged API server — surfaces as an error the
// operator can see instead of a spinner that never resolves.
const DefaultRequestTimeout = 30 * time.Second

// App owns the Wails application lifetime.
//
// It captures the runtime context Wails hands over at startup, which is the
// only handle through which the backend can emit events or drive the window.
// Every bound API derives its per-call context from it, so all in-flight work
// is cancelled when the window closes.
//
// App also implements ports.EventPublisher: domain events raised deep in the
// application layer come back out here and become Wails events.
//
// It is safe for concurrent use.
type App struct {
	logger         *slog.Logger
	requestTimeout time.Duration

	mu  sync.RWMutex
	ctx context.Context
}

// Compile-time proof that App can publish domain events.
var _ ports.EventPublisher = (*App)(nil)

// NewApp returns the application lifecycle handler.
//
// A requestTimeout of zero means DefaultRequestTimeout.
func NewApp(logger *slog.Logger, requestTimeout time.Duration) *App {
	if logger == nil {
		logger = slog.Default()
	}
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	return &App{
		logger:         logger.With(slog.String("adapter", "wails")),
		requestTimeout: requestTimeout,
	}
}

// OnStartup is the Wails startup hook. It records the runtime context.
func (a *App) OnStartup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	a.logger.Info("application started")
}

// OnShutdown is the Wails shutdown hook.
func (a *App) OnShutdown(_ context.Context) {
	a.mu.Lock()
	a.ctx = nil
	a.mu.Unlock()

	a.logger.Info("application stopped")
}

// runtimeContext returns the Wails runtime context, and whether it is
// available.
//
// It is unavailable before OnStartup and after OnShutdown — a narrow window,
// but one an event published during teardown lands in.
func (a *App) runtimeContext() (context.Context, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.ctx == nil {
		return nil, false
	}
	return a.ctx, true
}

// requestContext derives a bounded context for one call from the frontend.
//
// The caller must always call the returned cancel func, conventionally with
// defer, so the timer is released as soon as the call returns.
//
// Before startup it falls back to context.Background rather than failing: it
// keeps the bound APIs usable from tests that never run a window.
func (a *App) requestContext() (context.Context, context.CancelFunc) {
	parent, ok := a.runtimeContext()
	if !ok {
		parent = context.Background()
	}
	return context.WithTimeout(parent, a.requestTimeout)
}

// Publish delivers a domain event to the frontend over the Wails event bus.
//
// The ctx parameter is the *request* context of whichever use case raised the
// event; it is deliberately ignored, because emitting must use the
// application-lifetime context Wails supplied. Publishing on a request context
// would break the moment that request completed.
//
// Failures are logged and dropped: an event is a notification, and no use case
// should fail because the UI was not listening.
func (a *App) Publish(_ context.Context, event domain.DomainEvent) {
	if event == nil {
		return
	}

	runtimeCtx, ok := a.runtimeContext()
	if !ok {
		a.logger.Debug("dropping event raised outside the application lifetime",
			slog.String("event", string(event.Name())))
		return
	}

	payload, ok := toEventPayload(event)
	if !ok {
		a.logger.Warn("dropping event with no wire representation",
			slog.String("event", string(event.Name())))
		return
	}

	wailsruntime.EventsEmit(runtimeCtx, string(event.Name()), payload)
}

// emit sends an event to the frontend.
//
// This is a low-level helper used by both Publish (for domain events) and
// the management API (for log streaming). It handles the Wails runtime call
// and logs any errors.
func (a *App) emit(name string, payload any) {
	runtimeCtx, ok := a.runtimeContext()
	if !ok {
		a.logger.Debug("dropping event, app context not initialized",
			slog.String("event", name))
		return
	}

	wailsruntime.EventsEmit(runtimeCtx, name, payload)
}

// toEventPayload converts a domain event into its wire payload.
//
// An unknown event type returns false rather than being emitted with a nil
// payload, so a new domain event that nobody taught this adapter about shows
// up as a log line instead of an undefined value in the frontend.
func toEventPayload(event domain.DomainEvent) (any, bool) {
	switch e := event.(type) {
	case domain.ClusterConnected:
		return ClusterConnectedEvent{
			Cluster: toCluster(e.Cluster),
			At:      formatTime(e.OccurredAt()),
		}, true

	case domain.ClusterUnreachable:
		return ClusterUnreachableEvent{
			ClusterID: e.ClusterID.String(),
			Reason:    e.Reason,
			At:        formatTime(e.OccurredAt()),
		}, true

	default:
		return nil, false
	}
}
