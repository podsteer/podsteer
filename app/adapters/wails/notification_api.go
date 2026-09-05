package wails

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notificationActivatedEvent is emitted when somebody clicks a notification.
//
// The frontend brings the named cluster's tab to the front and opens its
// overview, which is where findings live — see App.svelte.
const notificationActivatedEvent = "notification:activated"

// maxNotificationBodyBytes caps the body PodSteer will hand the OS.
//
// Every platform truncates a long notification anyway, and the point of the
// cap is not the platform's limit: it is that a body this long is a body that
// has started listing things, and what a finding lists is object names. See
// NotificationRequest.
const maxNotificationBodyBytes = 240

// errEmptyNotification is raised when a notification arrives with no title.
//
// A notification with nothing to say is a notification nobody can act on, and
// on macOS it still occupies a slot in Notification Centre.
var errEmptyNotification = errors.New("a notification needs a title")

// errNotificationTooLong is raised when a body exceeds maxNotificationBodyBytes.
//
// Refused rather than truncated. Truncating would deliver most of whatever
// the caller was about to list, which is the failure mode the cap exists to
// prevent — a refusal is visible in a test and in the log, and a shortened
// sentence is not.
var errNotificationTooLong = errors.New("that notification body is too long to send")

// errNotificationUnavailable is raised when the platform will not deliver.
var errNotificationUnavailable = errors.New("this platform cannot show desktop notifications")

// NotificationRequest is one desktop notification, as the frontend asks for it.
//
// EVERY FIELD HERE IS CHOSEN SO THAT NO OBJECT NAME CAN TRAVEL, and that is a
// stricter rule than it looks. A desktop notification is not ephemeral: macOS
// keeps delivered notifications in Notification Centre, which is a database on
// disk, and a Linux notification daemon may log what it showed. So the same
// no-object-names commitment SECURITY.md makes about files applies here in
// full — a notification is a write.
//
// What that buys is that the fields are deliberately few. Title and Body carry
// a finding's TITLE, which is a rule's own name ("CrashLoopBackOff") written
// in this repository, and a count. There is no field for a namespace, a pod,
// a node or a workload, and adding one is the change this comment exists to
// make somebody argue for — notification_api_test.go asserts the field set
// against a literal list for exactly that reason, the way
// settingsFile.test.ts does for the export.
//
// ClusterID is a kubeconfig CONTEXT NAME. It is the one cluster-shaped thing
// that travels, on the same terms the settings file lets it travel: it is a
// handle the operator's own machine already gives them and it identifies
// nothing inside any cluster.
type NotificationRequest struct {
	// ID identifies this notification to the OS, so a later one can replace
	// it rather than stacking beside it.
	ID string `json:"id"`
	// Title is the headline.
	Title string `json:"title"`
	// Body is the sentence under it. May be empty.
	Body string `json:"body"`
	// ClusterID is the tab to bring forward when the notification is clicked.
	ClusterID string `json:"clusterId"`
}

// NotificationCapability is what the platform will actually do.
//
// THE TWO HALVES ARE SEPARATE, and collapsing them would be the mistake this
// codebase refuses to make elsewhere (MetricsStatus, ClusterReadStatus,
// PodGraph.Bounded): "this build cannot show notifications at all" and "the
// operator has denied this application permission" call for opposite
// sentences — one is a fact about the platform that no setting changes, and
// the other is fixed in the operating system's own preferences.
type NotificationCapability struct {
	// Supported reports whether this build and platform can deliver at all.
	Supported bool `json:"supported"`
	// Authorised reports whether the operating system will let this
	// application post one. On platforms with no notion of it, true.
	Authorised bool `json:"authorised"`
}

// NotificationAPI shows OS notifications, and does nothing else.
//
// It is a bound service rather than a runtime call the frontend makes
// directly because the webview has no route to the desktop: Wails' notification
// support is Go-side (UNUserNotificationCenter on macOS, toast XML on
// Windows, D-Bus on Linux), and the browser Notification API the page could
// otherwise reach is exactly the sort of thing the CSP and the no-network
// commitment exist to keep out of the page.
//
// IT IS ALSO THE ONLY PART OF THAT SUPPORT THE PAGE CAN REACH. Wails v3
// ships notifications as a service of its own, whose exported methods would
// all be bound if it were registered — the removal and category-management
// surface included. So App starts it by hand and keeps the handle, and these
// three methods are the whole of what crosses the bridge. See
// App.StartNotifications.
//
// WHAT IT DOES NOT DO IS DECIDE ANYTHING. Whether a finding is new, whether
// it is snoozed, whether the operator asked for this at all and whether one
// has been sent too recently are all decided in the frontend, beside the
// assessment diff those questions are about — see web/src/lib/notify.ts. This
// end is the delivery mechanism and the platform's own honesty about what it
// can deliver.
type NotificationAPI struct {
	app    *App
	logger *slog.Logger
}

// NewNotificationAPI returns the bound notification API.
func NewNotificationAPI(app *App, logger *slog.Logger) (*NotificationAPI, error) {
	if app == nil {
		return nil, fmt.Errorf("wails: NotificationAPI requires an App")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &NotificationAPI{
		app:    app,
		logger: logger.With(slog.String("api", "notification")),
	}, nil
}

// Capability reports what this platform will actually do.
//
// Asked when the Settings pane opens, so the switch can say why it will do
// nothing rather than sitting there looking as though it works — the same job
// alertPlayer.available does for the sound.
func (n *NotificationAPI) Capability() NotificationCapability {
	service, ok := n.app.notificationService()
	if !ok {
		// Before the window exists, after it has gone, or on a machine where
		// the platform service refused to start — which is the v3 form of
		// v2's IsNotificationAvailable, since the backend reports that by
		// failing its own startup. Not an error: the pane is asking a
		// question about a running application.
		return NotificationCapability{}
	}

	authorised, err := service.CheckNotificationAuthorization()
	if err != nil {
		// SUPPORTED, AND NOT AUTHORISED. Failing to ask is not the operator
		// saying no, but nothing can be posted either way, and the pane's two
		// sentences are "this build cannot" and "your system has not allowed
		// it" — the second is the one that sends somebody somewhere useful.
		// The diagnosis goes to the log rather than into the answer, which is
		// a verdict rather than a place to put an error string nobody in the
		// interface could act on.
		n.logger.Debug("could not read the notification authorisation",
			slog.String("error", err.Error()))
		return NotificationCapability{Supported: true}
	}

	return NotificationCapability{Supported: true, Authorised: authorised}
}

// Request asks the operating system for permission to post notifications.
//
// Called when the operator turns the preference ON, and never before: on
// macOS this is a visible system prompt, and one that arrives unprompted is
// one people deny. On platforms with no such concept it is a no-op returning
// true.
func (n *NotificationAPI) Request() (bool, error) {
	service, ok := n.app.notificationService()
	if !ok {
		return false, apiError(n.logger, "Request", errNotificationUnavailable)
	}

	granted, err := service.RequestNotificationAuthorization()
	if err != nil {
		return false, apiError(n.logger, "Request", err)
	}
	return granted, nil
}

// Notify posts one notification.
//
// NOTHING IS LOGGED BUT THE FACT OF IT. The title is a finding's own rule
// name and could reasonably be logged, but a log line that quoted the body
// would be a second copy of whatever the body carried, in a file, which is
// the shape of mistake this whole feature is fenced against. The count of
// notifications is what an engineer debugging this needs.
//
// DO-NOT-DISTURB IS THE OPERATING SYSTEM'S, and this is where that is honest
// rather than implemented: macOS Focus, Windows Focus Assist and a GNOME or
// KDE notification daemon's own quiet mode all apply AFTER delivery, in the
// notification centre itself, and none of them is readable through any API
// Wails exposes. So PodSteer posts, and the platform decides whether to
// present — which is the behaviour every well-behaved desktop application
// has. Inventing a do-not-disturb check here would mean guessing at a state
// this process cannot see, and guessing wrong in the direction that silences
// an alarm somebody asked for.
func (n *NotificationAPI) Notify(request NotificationRequest) error {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return apiError(n.logger, "Notify", errEmptyNotification)
	}

	body := strings.TrimSpace(request.Body)
	if len(body) > maxNotificationBodyBytes {
		return apiError(n.logger, "Notify", fmt.Errorf(
			"%w: it is %d bytes; PodSteer sends at most %d",
			errNotificationTooLong, len(body), maxNotificationBodyBytes))
	}

	service, ok := n.app.notificationService()
	if !ok {
		return apiError(n.logger, "Notify", errNotificationUnavailable)
	}

	id := strings.TrimSpace(request.ID)
	if id == "" {
		id = "podsteer-finding"
	}

	err := service.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  body,
		// The click target, and the ONLY thing carried through: a kubeconfig
		// context name, which the OS hands back on activation. Nothing about
		// the objects the finding names goes into it, so a notification
		// sitting in Notification Centre says no more than it displayed.
		Data: map[string]any{"clusterId": request.ClusterID},
	})
	if err != nil {
		return apiError(n.logger, "Notify", err)
	}

	n.logger.Debug("posted a desktop notification")
	return nil
}
