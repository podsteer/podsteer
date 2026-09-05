package wails

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// A notification cannot be delivered in a test: there is no window, so there
// is no Wails runtime context and nothing to post to. What IS testable here
// is the part that decides whether to try — and the SHAPE of the request,
// which is the half that carries the no-object-names commitment.

func newTestNotificationAPI(t *testing.T) *NotificationAPI {
	t.Helper()

	api, err := NewNotificationAPI(NewApp(slog.New(slog.DiscardHandler), 0), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewNotificationAPI() error = %v", err)
	}
	return api
}

func TestNewNotificationAPIRequiresAnApp(t *testing.T) {
	t.Parallel()

	// The App is the only route to a runtime context. Without it every method
	// would silently do nothing, which is worse than refusing to be built.
	if _, err := NewNotificationAPI(nil, slog.Default()); err == nil {
		t.Fatal("NewNotificationAPI(nil) error = nil, want a refusal")
	}
}

func TestNotificationRequestCarriesNoObjectNames(t *testing.T) {
	t.Parallel()

	// THE GUARD, and it is the same shape settingsFile.test.ts uses for the
	// export: a LITERAL field list, so a namespace, a pod name or a node name
	// cannot join this struct without somebody editing this list and arguing
	// for it. A desktop notification is a write — macOS keeps delivered ones
	// in Notification Centre, which is on disk — so the commitment SECURITY.md
	// makes about files applies to it in full.
	//
	// clusterId is the one cluster-shaped field, and it is a kubeconfig
	// CONTEXT NAME on exactly the terms the settings file lets one travel: a
	// handle the operator's own machine already gives them, identifying
	// nothing inside any cluster.
	want := []string{"id", "title", "body", "clusterId"}

	shape := reflect.TypeOf(NotificationRequest{})
	got := make([]string, 0, shape.NumField())
	for i := range shape.NumField() {
		tag, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		got = append(got, tag)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NotificationRequest fields = %v, want exactly %v", got, want)
	}
}

func TestNotifyRefusesANotificationWithNoTitle(t *testing.T) {
	t.Parallel()

	// A notification with nothing to say still occupies a slot in the
	// operating system's notification centre, so it is refused rather than
	// posted blank.
	api := newTestNotificationAPI(t)

	err := api.Notify(NotificationRequest{Title: "   ", Body: "something happened"})
	if err == nil {
		t.Fatal("Notify() error = nil, want a refusal")
	}
	if !strings.HasPrefix(err.Error(), "["+string(CodeInvalidInput)+"]") {
		t.Fatalf("Notify() error = %v, want it classified as invalid input", err)
	}
}

func TestNotifyRefusesABodyLongEnoughToBeListingThings(t *testing.T) {
	t.Parallel()

	// REFUSED, NOT TRUNCATED. A body this long is a body that has started
	// listing objects, and truncating would deliver most of the list — the
	// exact failure the cap exists to prevent, and one that leaves no trace
	// anybody would notice.
	api := newTestNotificationAPI(t)

	err := api.Notify(NotificationRequest{
		Title: "New critical findings",
		Body:  strings.Repeat("x", maxNotificationBodyBytes+1),
	})
	if err == nil {
		t.Fatal("Notify() error = nil, want a refusal")
	}
	if !strings.HasPrefix(err.Error(), "["+string(CodeInvalidInput)+"]") {
		t.Fatalf("Notify() error = %v, want it classified as invalid input", err)
	}
}

func TestNotifyWithoutAWindowIsAnErrorRatherThanAPanic(t *testing.T) {
	t.Parallel()

	// The narrow window an event published during teardown lands in, and the
	// whole of a `go test` run. Every runtime call here needs a context that
	// does not exist, so the method has to say so rather than dereference it.
	api := newTestNotificationAPI(t)

	if err := api.Notify(NotificationRequest{Title: "New critical findings"}); err == nil {
		t.Fatal("Notify() error = nil, want it refused with no window running")
	}
}

func TestNotificationCapabilityReportsNothingWithoutAWindow(t *testing.T) {
	t.Parallel()

	// NOT AN ERROR. The Settings pane asks this question of a running
	// application; asked before one exists, the honest answer is that nothing
	// is supported, and the pane says the switch will do nothing.
	api := newTestNotificationAPI(t)

	got := api.Capability()
	if got.Supported {
		t.Fatalf("Capability() = %+v, want nothing supported with no window", got)
	}
	// Unsupported, and NOT reported as denied: the operator has refused
	// nothing here, and saying they had would send somebody into their system
	// preferences to grant a permission that was never asked for.
	if got.Authorised {
		t.Fatalf("Capability() = %+v, want authorisation left unclaimed", got)
	}
}

func TestNotificationStartAndStopAreSafeWithoutAWindow(t *testing.T) {
	t.Parallel()

	// Both run from the composition root's lifecycle hooks, and both must be
	// inert when there is no application — a startup that failed before the
	// window existed must not take the shutdown path down with it.
	//
	// They live on App and NOT on the bound service on purpose, which this
	// test also pins by calling them there: Wails binds every exported method
	// of every registered service, so a Start here would be a webview-callable
	// way to re-register the click handler, and a Stop a webview-callable way
	// to tear the platform's connection down under the application. App is not
	// a service, which is also why v3's own notification service is started by
	// hand rather than registered — see App.StartNotifications.
	app := NewApp(slog.New(slog.DiscardHandler), 0)
	app.StartNotifications()
	app.StopNotifications()
}
