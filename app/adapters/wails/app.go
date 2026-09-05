package wails

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// DefaultRequestTimeout bounds a single call from the frontend.
//
// It exists so a cluster that accepts a TCP connection and then goes quiet — a
// half-open VPN tunnel, a wedged API server — surfaces as an error the
// operator can see instead of a spinner that never resolves.
const DefaultRequestTimeout = 30 * time.Second

// MainWindowName is the name the composition root gives the one window.
//
// Wails v3 is a multi-window framework: every window is addressable, and
// App.Window.Current() answers with whichever one has focus — which is none
// at all when the application is hidden or minimised, exactly the state a
// notification click has to recover from. PodSteer has one window, so it is
// named and looked up by name; see App.mainWindow.
const MainWindowName = "main"

// App owns the Wails application lifetime.
//
// It holds the *application.App the composition root built, which is the only
// handle through which the backend can emit events, open a native dialog or
// drive the window. Every bound service derives its per-call context from the
// application context, so all in-flight work is cancelled when the window
// closes.
//
// App also implements ports.EventPublisher: domain events raised deep in the
// application layer come back out here and become Wails events.
//
// IT IS DELIBERATELY NOT A SERVICE. Wails v3 binds every exported method of
// every registered service, so keeping the lifecycle here — rather than on
// NotificationAPI, where it started — is what keeps StartNotifications and
// StopNotifications out of reach of the webview.
//
// It is safe for concurrent use.
type App struct {
	logger         *slog.Logger
	requestTimeout time.Duration

	mu  sync.RWMutex
	app *application.App
	// notifier is the platform notification service, started by
	// StartNotifications. Nil until then, and nil for ever on a machine that
	// cannot deliver — which is what NotificationAPI.Capability reports.
	notifier *notifications.NotificationService
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

// Attach records the Wails application.
//
// Called by the composition root immediately after application.New and before
// app.Run. The order is forced rather than chosen: the services this type
// serves are built BEFORE application.New, because they ARE its Services
// argument, so the handle they share cannot be a constructor parameter. Under
// Wails v2 the same handle arrived asynchronously in the OnStartup hook; here
// it exists as soon as the application is constructed, which is strictly
// earlier.
func (a *App) Attach(app *application.App) {
	a.mu.Lock()
	a.app = app
	a.mu.Unlock()

	a.logger.Info("application started")
}

// Detach drops the Wails application handle.
//
// Called from the shutdown hook, after everything that needs the runtime has
// had its turn. Events published after this point are dropped with a log line
// rather than reaching a webview that has gone.
func (a *App) Detach() {
	a.mu.Lock()
	a.app = nil
	a.mu.Unlock()

	a.logger.Info("application stopped")
}

// StartNotifications prepares the desktop notification service and registers
// the handler for somebody clicking one.
//
// THE SERVICE IS STARTED BY HAND RATHER THAN REGISTERED. Wails v3 ships
// notifications as an ordinary service, and a registered service has every
// exported method bound — which for this one would put
// RemoveAllDeliveredNotifications and the rest of its management surface
// within reach of the page. So it is constructed here and given its
// ServiceStartup directly; nothing about it reaches the webview except through
// NotificationAPI, which exposes three methods and decides nothing. This is
// the v3 shape of the same rule that put the lifecycle on App rather than on
// NotificationAPI under v2.
//
// Called from the composition root once the application exists.
// FAILING IS NOT FATAL and is deliberately not surfaced: a machine that will
// not initialise notifications is a machine that shows none, which is exactly
// what NotificationAPI.Capability then reports to the Settings pane. Refusing
// to start the application over an optional alarm would be absurd.
//
// NO AUTHORISATION IS REQUESTED HERE. On macOS the request is a visible
// system prompt, and firing one at somebody who has never asked for
// notifications — the preference is off by default — is precisely how an
// application gets denied permanently in its first minute.
// NotificationAPI.Request runs when the operator turns the preference on.
func (a *App) StartNotifications() {
	ctx, ok := a.runtimeContext()
	if !ok {
		return
	}

	service := notifications.New()
	if err := service.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		// The v3 equivalent of v2's IsNotificationAvailable: the platform
		// backend refuses to start when it cannot deliver — no notification
		// centre, or, on macOS, no bundle identifier because the binary is
		// running outside a .app.
		a.logger.Debug("desktop notifications are not available",
			slog.String("error", err.Error()))
		return
	}

	// Wails delivers the response on its own goroutine, so everything in here
	// is a runtime call or an event emit — both safe from one.
	service.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			a.logger.Debug("notification response carried an error",
				slog.String("error", result.Error.Error()))
			return
		}

		// THE WINDOW FIRST, then the event. A click on a notification is a
		// request to look at something, and an event that switched tabs
		// behind a hidden window would move the operator's application
		// without ever showing it to them.
		a.RaiseWindow()

		// Whatever came back, and nothing more: the notification carried one
		// kubeconfig context name and no object name, so there is nothing
		// else here to hand over. An empty one still raises the window,
		// because a click is still a request to look.
		cluster, _ := result.Response.UserInfo["clusterId"].(string)
		a.emit(notificationActivatedEvent, NotificationActivatedEvent{ClusterID: cluster})
	})

	a.mu.Lock()
	a.notifier = service
	a.mu.Unlock()
}

// StopNotifications releases whatever the platform held — on Linux, a D-Bus
// connection.
//
// Called from the shutdown hook beside the port-forward, node-shell and
// local-shell teardown, and for the reason those are there: PodSteer opened
// it, so PodSteer closes it.
func (a *App) StopNotifications() {
	a.mu.Lock()
	service := a.notifier
	a.notifier = nil
	a.mu.Unlock()

	if service == nil {
		return
	}
	if err := service.ServiceShutdown(); err != nil {
		a.logger.Debug("could not release the notification service",
			slog.String("error", err.Error()))
	}
}

// notificationService returns the started notification service, and whether
// there is one.
//
// Nil means this platform, or this launch, cannot deliver — which is the
// question NotificationAPI.Capability answers first.
func (a *App) notificationService() (*notifications.NotificationService, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.notifier == nil {
		return nil, false
	}
	return a.notifier, true
}

// wailsApp returns the Wails application, and whether it is available.
//
// It is unavailable before Attach and after Detach — a narrow window, but one
// an event published during teardown lands in — and in every test, which
// never builds one.
func (a *App) wailsApp() (*application.App, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.app == nil {
		return nil, false
	}
	return a.app, true
}

// RaiseWindow brings PodSteer's one window to the front, if there is one.
//
// Two callers, both of them somebody asking to look at the application: a
// click on a desktop notification, and a second launch that the single-instance
// lock turned into a request to raise the first. Both need the same three
// calls in the same order, and both are no-ops before the window exists.
//
// The window is found by NAME rather than through Window.Current, which
// answers with the FOCUSED window and therefore with nothing at all when the
// application is hidden or minimised — the exact state both callers are
// trying to leave. It is exported because the composition root wires the
// single-instance callback, and unreachable from the webview for the reason
// the rest of this type is: App is not a service.
func (a *App) RaiseWindow() {
	app, ok := a.wailsApp()
	if !ok {
		return
	}

	window, ok := app.Window.Get(MainWindowName)
	if !ok {
		return
	}

	window.UnMinimise()
	window.Show()
	window.Focus()
}

// runtimeContext returns the application-lifetime context, and whether it is
// available.
func (a *App) runtimeContext() (context.Context, bool) {
	app, ok := a.wailsApp()
	if !ok {
		return nil, false
	}
	return app.Context(), true
}

// requestContext derives a bounded context for one call from the frontend.
//
// The caller must always call the returned cancel func, conventionally with
// defer, so the timer is released as soon as the call returns.
//
// Before the application exists it falls back to context.Background rather
// than failing: it keeps the bound services usable from tests that never run
// a window.
func (a *App) requestContext() (context.Context, context.CancelFunc) {
	parent, ok := a.runtimeContext()
	if !ok {
		parent = context.Background()
	}
	return context.WithTimeout(parent, a.requestTimeout)
}

// bulkTimeoutCap is the most a bulk action may take however large the
// selection: a wedged API server must not pin the review dialog for an hour
// because somebody selected a page of two hundred rows.
const bulkTimeoutCap = 10 * time.Minute

// requestContextFor derives a context for a call that is really `requests`
// single calls — a bulk action over that many objects.
//
// One request timeout each rather than one for the lot: the whole point of
// the bound is that a quiet cluster surfaces as an error, and a bulk delete
// of fifty pods against a slow cluster that answers each in a second is not
// quiet, it is fifty seconds of work that a single 30s timeout would cut
// off halfway with the rest never attempted. Capped, so the bound still
// means something.
func (a *App) requestContextFor(requests int) (context.Context, context.CancelFunc) {
	parent, ok := a.runtimeContext()
	if !ok {
		parent = context.Background()
	}
	timeout := a.requestTimeout * time.Duration(max(requests, 1))
	if timeout > bulkTimeoutCap {
		timeout = bulkTimeoutCap
	}
	return context.WithTimeout(parent, timeout)
}

// Publish delivers a domain event to the frontend over the Wails event bus.
//
// The ctx parameter is the *request* context of whichever use case raised the
// event; it is deliberately ignored, because an emit is an application-wide
// act and tying it to a request would break the moment that request completed.
//
// Failures are logged and dropped: an event is a notification, and no use case
// should fail because the UI was not listening.
func (a *App) Publish(_ context.Context, event domain.DomainEvent) {
	if event == nil {
		return
	}

	app, ok := a.wailsApp()
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

	app.Event.Emit(string(event.Name()), payload)
}

// emit sends an event to the frontend.
//
// This is a low-level helper used by both Publish (for domain events) and
// the management API (for log streaming).
func (a *App) emit(name string, payload any) {
	app, ok := a.wailsApp()
	if !ok {
		a.logger.Debug("dropping event, app context not initialized",
			slog.String("event", name))
		return
	}

	app.Event.Emit(name, payload)
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
