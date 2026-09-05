// Package cmd is the PodSteer composition root.
//
// This is the one place that knows every layer: it reads configuration, builds
// the concrete adapters, injects them into the use cases and hands the result
// to Wails. Nothing else in the codebase constructs a dependency, which is
// what keeps the arrows in the hexagon pointing inward.
//
// # Why this is not package main
//
// The Wails v2 CLI compiled the package in the project root — `wails build` ran
// `go build` with its working directory set there and no package argument — so
// the `main` package had to live at the repository root. That root main.go is
// a three-line shim that calls Main below; every line of real wiring is here,
// under app/, where the project layout puts it.
//
// Wails v3 imposes no such rule, so this is now inherited rather than forced.
// See the root main.go for what moving it would involve.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	// Aliased because this repository has an `application` package of its own —
	// the use-case layer — and the composition root is the one file that
	// names both.
	wailsapp "github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/podsteer/podsteer/app/adapters/archive"
	"github.com/podsteer/podsteer/app/adapters/assets"
	historystore "github.com/podsteer/podsteer/app/adapters/history"
	"github.com/podsteer/podsteer/app/adapters/k8s"
	"github.com/podsteer/podsteer/app/adapters/localshell"
	"github.com/podsteer/podsteer/app/adapters/macwindow"
	"github.com/podsteer/podsteer/app/adapters/shellpath"
	"github.com/podsteer/podsteer/app/adapters/updates"
	wailsadapter "github.com/podsteer/podsteer/app/adapters/wails"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/config"
	"github.com/podsteer/podsteer/app/domain"
)

// trafficLightVerticalNudge shifts macOS's traffic lights so they sit
// centred in this app's own tab bar (ClusterTabs.svelte), rather than where
// AppKit puts them by default for a standard, shorter title bar.
//
// This is a live-tuned constant, not a derived one — see
// macwindow.NudgeTrafficLights for why there is no formula that gets there
// from the tab bar's height alone. If the tab bar's height changes, or this
// still looks off on a given macOS version, adjust this number and rebuild;
// there is nothing else to change.
const trafficLightVerticalNudge = 6.0

// Main starts PodSteer and terminates the process on failure.
//
// It is the only function in the codebase that calls os.Exit, so every other
// layer stays testable and composable.
func Main() {
	if err := dispatch(os.Args[1:]); err != nil {
		// The logger may not exist yet if configuration itself failed, so this
		// deliberately writes to stderr directly.
		fmt.Fprintf(os.Stderr, "podsteer: %v\n", err)
		os.Exit(1)
	}
}

// dispatch chooses between the desktop application and the subcommands.
//
// NO ARGUMENTS MEANS THE WINDOW, and that is not merely the default: it is
// how the application is actually started. A double-click in Finder, a Dock
// icon, a desktop launcher and `brew install --cask podsteer` all run this
// binary with nothing after its name, so a required flag, a usage message or
// a prompt in front of that path would make PodSteer unstartable for
// everybody who has never opened a terminal. A subcommand is therefore
// additive: it is reached only when the first argument names it, and every
// other launch behaves exactly as it did before this existed.
//
// Under Wails v2 there was a second, mechanical reason — binding generation
// compiled and RAN this binary argument-free — and it is gone: `wails3
// generate bindings` reads the source. The rule outlived it; see
// main_test.go.
func dispatch(args []string) error {
	chosen, rest, err := route(args)
	if err != nil {
		return err
	}

	switch chosen {
	case commandMCP:
		return runMCP(rest)
	default:
		return run()
	}
}

// command is what a set of arguments asks for.
type command int

const (
	// commandWindow is the desktop application, and the zero value: the
	// argument-free launch must never depend on this file adding a case.
	commandWindow command = iota
	// commandMCP is the Model Context Protocol server on stdio.
	commandMCP
)

// route decides which command the arguments name.
//
// Split from dispatch so the rule above — no arguments means the window — is
// something a test can assert without starting a window, which is exactly
// what a test of the binding-generation path cannot do.
func route(args []string) (command, []string, error) {
	if len(args) == 0 {
		return commandWindow, nil, nil
	}

	switch args[0] {
	case "mcp":
		return commandMCP, args[1:], nil
	default:
		// Refused rather than ignored. Silently opening the window on an
		// argument nobody recognised would answer a mistyped subcommand with
		// a desktop application, which is not what anyone piping stdio at
		// this binary is waiting for.
		return commandWindow, nil, fmt.Errorf("unknown command %q (try: podsteer mcp --help)", args[0])
	}
}

// run wires the application together and blocks until the window closes.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	logger := newLogger(cfg.Log)
	// Set as default so any package that falls back to slog.Default — the
	// optional Logger fields on the services — inherits the same handler.
	slog.SetDefault(logger)

	logger.Info("starting podsteer",
		slog.String("version", cfg.App.Version),
		slog.String("kubeconfig", kubeconfigLabel(cfg.Kubernetes.KubeconfigPath)))

	// A kubeconfig context can name a credential plugin — `aws eks get-token`
	// for EKS, `gke-gcloud-auth-plugin` for GKE — which client-go resolves
	// through PATH. A .app launched from Finder or by Homebrew inherits
	// launchd's environment, which has no Homebrew directory in it, so every
	// managed cluster failed to connect from an installed build while working
	// perfectly under `make run`.
	//
	// ALONGSIDE THE WINDOW RATHER THAN BEFORE IT. Asking the login shell takes
	// about a second, and a second of blank window on every launch is a bad
	// trade for something only the first connection needs. The channel is
	// handed to the Kubernetes adapter, which waits on it when it builds its
	// first client — by which time the operator has usually spent longer than
	// that choosing a cluster.
	envReady := make(chan struct{})
	go func() {
		defer close(envReady)
		logger.Info("resolved PATH", slog.String("result", shellpath.Resolve(context.Background())))
	}()

	// --- Driven (outbound) adapters -------------------------------------
	//
	// Built first because everything inward depends on them. The Kubernetes
	// adapter performs no I/O here: a machine with an unreachable cluster, or
	// none at all, still reaches a usable window.

	kubernetes := k8s.New(k8s.Config{
		KubeconfigPath: cfg.Kubernetes.KubeconfigPath,
		KubeconfigDir:  cfg.Kubernetes.KubeconfigDir,
		QPS:            cfg.Kubernetes.QPS,
		Burst:          cfg.Kubernetes.Burst,
		UserAgent:      fmt.Sprintf("%s/%s", cfg.App.Name, cfg.App.Version),
		EnvReady:       envReady,
		LiveWatch:      cfg.Kubernetes.LiveWatch,
	}, logger)

	// Shells on the operator's OWN machine — the local terminal and the coding
	// agent it can launch. It reaches no cluster, so it is not a Kubernetes
	// adapter and holds no client; the one thing it needs from the cluster
	// side is which kubeconfig files to name in KUBECONFIG, and it takes that
	// as a function so a file dropped into the kubeconfig directory is seen by
	// the next shell without a restart.
	//
	// It also inherits the PATH the goroutine above adopts, which is what
	// makes both a Homebrew kubectl and a Homebrew coding agent findable from
	// a Dock launch.
	localShells := localshell.New(localshell.Config{
		KubeconfigFiles: kubernetes.KubeconfigFiles,
		Shell:           shellpath.LoginShell,
	}, logger)

	// The Wails lifecycle handler doubles as the outbound event publisher, so
	// it is constructed before the use cases that publish through it.
	desktop := wailsadapter.NewApp(logger, cfg.Kubernetes.RequestTimeout)

	// --- Application (use cases) -----------------------------------------
	//
	// The registry holds every open cluster — one per tab — and the catalog
	// holds what each of them can show, built-ins plus whatever CRDs discovery
	// found. Both are shared by the services, which is why they are built here
	// rather than inside any one of them.

	registry := application.NewRegistry()
	catalog := domain.NewCatalog()

	clusterService, err := application.NewClusterService(application.ClusterServiceDeps{
		Kubeconfig: kubernetes,
		Cluster:    kubernetes,
		Workloads:  kubernetes,
		Metrics:    kubernetes,
		Events:     desktop,
		Registry:   registry,
		Catalog:    catalog,
		Logger:     logger,
		// The adapter's own caches are released here on disconnect, which is
		// the composition root's job precisely because Invalidate is not a
		// port — it exists to serve the adapter's caching, not the domain.
		Invalidator: kubernetes,
	})
	if err != nil {
		return fmt.Errorf("wiring cluster service: %w", err)
	}

	workloadService, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Workloads: kubernetes,
		Metrics:   kubernetes,
		Registry:  registry,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("wiring workload service: %w", err)
	}

	browseService, err := application.NewBrowseService(application.BrowseServiceDeps{
		Resources: kubernetes,
		Events:    kubernetes,
		Registry:  registry,
		Catalog:   catalog,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("wiring browse service: %w", err)
	}

	// The fleet reads through the two services above rather than the
	// adapter, so a cross-cluster row is exactly the row that cluster's own
	// tab would show, and the read cache coalesces the two.
	fleetService, err := application.NewFleetService(application.FleetServiceDeps{
		Workloads: workloadService,
		Events:    browseService,
		Registry:  registry,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("wiring fleet service: %w", err)
	}

	// The RBAC explorer. Every call it makes is a read, and every one of
	// them happens because somebody pressed something — it is deliberately
	// not wired into anything that runs on a timer.
	rbacService, err := application.NewRBACService(application.RBACServiceDeps{
		RBAC:     kubernetes,
		Registry: registry,
		Logger:   logger,
	})
	if err != nil {
		return fmt.Errorf("wiring rbac service: %w", err)
	}

	overviewService, err := application.NewOverviewService(application.OverviewServiceDeps{
		Cluster:   kubernetes,
		Workloads: kubernetes,
		Events:    kubernetes,
		Metrics:   kubernetes,
		APIs:      kubernetes,
		Registry:  registry,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("wiring overview service: %w", err)
	}

	// Sampling records what each open cluster looks like over time, so the
	// dashboard can show a trend rather than an instant. It writes to the
	// operator's own config directory and nowhere else; a store that cannot
	// be located degrades to recording nothing rather than failing startup,
	// because a chart is not worth refusing to open the application over.
	historyDir, err := historystore.DefaultDir()
	if err != nil {
		logger.Warn("history will not be recorded", slog.String("error", err.Error()))
	}

	historyService, err := application.NewHistoryService(application.HistoryServiceDeps{
		History:      historystore.New(historyDir),
		Overview:     overviewService,
		Registry:     registry,
		SettingsPath: filepath.Join(filepath.Dir(historyDir), "history.json"),
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("wiring history service: %w", err)
	}
	// Stopped before the process exits, and waited for: the sampler writes
	// files, so returning while it is mid-write would truncate one.
	defer historyService.Close()

	managementService, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: kubernetes,
		// The same registry clusterService reads and ClusterAPI.SetReadOnly
		// writes: a policy set through one has to be enforced by the other,
		// or a cluster the operator marked read-only would still accept
		// writes issued through this service.
		Registry: registry,
		Logger:   logger,
		// The local half of a file copy. Everything that decides what a
		// container's tar stream may do to this machine lives behind it,
		// and the ceilings come from configuration so an operator who
		// means to move more can say so.
		Archive: archive.Local{},
		TransferLimits: domain.TransferLimits{
			MaxBytes:   cfg.FileCopy.MaxBytes,
			MaxEntries: cfg.FileCopy.MaxEntries,
		},
	})
	if err != nil {
		return fmt.Errorf("wiring management service: %w", err)
	}

	// The on-request inspections: a reachability probe, and an image report.
	// The registry goes in for one method only — an in-cluster probe runs a
	// command in somebody's container, which is write-shaped whatever it
	// reads, so it is refused on a cluster the operator marked read-only and
	// audited like every other exec here.
	inspectService, err := application.NewInspectService(application.InspectServiceDeps{
		Inspect:  kubernetes,
		Registry: registry,
		Logger:   logger,
	})
	if err != nil {
		return fmt.Errorf("wiring inspect service: %w", err)
	}

	// --- Driving (inbound) adapters ---------------------------------------
	//
	// These depend on the inbound ports, not on the concrete services: the
	// bindings would work just as well against a fake implementation.

	clusterAPI, err := wailsadapter.NewClusterAPI(clusterService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring cluster API: %w", err)
	}

	workloadAPI, err := wailsadapter.NewWorkloadAPI(workloadService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring workload API: %w", err)
	}

	browseAPI, err := wailsadapter.NewBrowseAPI(
		browseService, browseService, browseService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring browse API: %w", err)
	}

	overviewAPI, err := wailsadapter.NewOverviewAPI(overviewService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring overview API: %w", err)
	}

	fleetAPI, err := wailsadapter.NewFleetAPI(fleetService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring fleet API: %w", err)
	}

	rbacAPI, err := wailsadapter.NewRBACAPI(rbacService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring rbac API: %w", err)
	}

	// The Kubernetes adapter is the port-forward AND the node-shell transport:
	// both track a resource PodSteer created (a bound socket, a privileged
	// pod) and both must tear it down where the record lives, so they share
	// the adapter rather than a service layer that would only forward calls.
	managementAPI, err := wailsadapter.NewManagementAPI(managementService, kubernetes, kubernetes, workloadService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring management API: %w", err)
	}

	terminalAPI, err := wailsadapter.NewTerminalAPI(managementService, kubernetes, localShells, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring terminal API: %w", err)
	}

	fileCopyAPI, err := wailsadapter.NewFileCopyAPI(managementService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring file copy API: %w", err)
	}

	inspectAPI, err := wailsadapter.NewInspectAPI(inspectService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring inspect API: %w", err)
	}

	historyAPI, err := wailsadapter.NewHistoryAPI(historyService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring history API: %w", err)
	}

	// The update check. Its adapter is the ONLY thing in PodSteer that talks
	// to anything but a cluster, and it acts only when the interface asks —
	// there is no timer here and nothing on the startup path. It sends no
	// identifier and is off entirely under PODSTEER_UPDATE_CHECK=false.
	updateService := application.NewUpdateService(updates.NewClient(), cfg.App.Version, logger)

	updateAPI, err := wailsadapter.NewUpdateAPI(updateService, logger)
	if err != nil {
		return fmt.Errorf("wiring update API: %w", err)
	}

	systemAPI, err := wailsadapter.NewSystemAPI(cfg.App.Name, cfg.App.Version, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring system API: %w", err)
	}

	// Desktop notifications. It decides nothing — whether a finding is new,
	// snoozed, wanted or too recent is settled in the frontend beside the
	// assessment diff those questions are about — so this is the delivery
	// mechanism and the platform's own honesty about what it can deliver.
	notificationAPI, err := wailsadapter.NewNotificationAPI(desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring notification API: %w", err)
	}

	frontend, err := assets.FS()
	if err != nil {
		return err
	}

	// --- Application and window --------------------------------------------
	//
	// Wails v3 separates the two: the application holds the services, the
	// assets and the process lifetime, and a window is a thing it is asked to
	// open. PodSteer opens exactly one and names it, because every runtime
	// call in v3 is made on a window and Window.Current answers with the
	// focused one — which is none at all when the application is hidden.

	desktopApp := wailsapp.New(wailsapp.Options{
		Name: cfg.App.Title,
		// Shown in the platform's own About box, which is where v2's
		// mac.AboutInfo.Message went.
		Description: "A fast, native Kubernetes client.\nVersion " + cfg.App.Version,

		// Wails' OWN logging, not the application's. It shares the handler so
		// there is one stream to read, and sits at warn because the v3 asset
		// server otherwise logs a line per request — every chunk of every
		// bundle, on every launch.
		Logger:   logger,
		LogLevel: slog.LevelWarn,

		Assets: wailsapp.AssetOptions{
			Handler:        wailsapp.AssetFileServerFS(frontend),
			DisableLogging: true,
		},

		// Everything registered here becomes callable from TypeScript, and
		// `wails3 generate bindings` reads THIS LIST by static analysis to
		// produce the declarations — it no longer compiles and runs the
		// binary to find them out. Every exported method of every service is
		// bound, which is why App itself is not one: see
		// wails.App.StartNotifications.
		Services: []wailsapp.Service{
			wailsapp.NewService(clusterAPI),
			wailsapp.NewService(workloadAPI),
			wailsapp.NewService(browseAPI),
			wailsapp.NewService(overviewAPI),
			wailsapp.NewService(fleetAPI),
			wailsapp.NewService(rbacAPI),
			wailsapp.NewService(historyAPI),
			wailsapp.NewService(managementAPI),
			wailsapp.NewService(terminalAPI),
			wailsapp.NewService(fileCopyAPI),
			wailsapp.NewService(inspectAPI),
			wailsapp.NewService(systemAPI),
			wailsapp.NewService(updateAPI),
			wailsapp.NewService(notificationAPI),
		},

		// Only one PodSteer should hold the kubeconfig and its client caches;
		// a second launch raises the existing window instead of opening a
		// second one. Under v2 the raise was the framework's; here the
		// callback is where it happens, which is also the only way the second
		// process's launch is observable at all.
		SingleInstance: &wailsapp.SingleInstanceOptions{
			UniqueID: "com.podsteer.desktop",
			OnSecondInstanceLaunch: func(wailsapp.SecondInstanceData) {
				desktop.RaiseWindow()
			},
		},

		Mac: wailsapp.MacOptions{
			// v3 keeps an application alive with no windows — it is built for
			// tray and multi-window applications. PodSteer has one window, so
			// closing it means quitting, which is what v2 did unconditionally.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},

		OnShutdown: func() {
			// Forwards first: each one holds a local socket, and a process
			// that exits without releasing them leaves ports bound until the
			// operating system reaps them. That is the orphaned-port
			// complaint every competing client has an issue open about, and
			// the fix is to close them rather than to hope.
			kubernetes.StopAllPortForwards()
			// Node shells next, and for a sharper reason than a leaked socket:
			// each is a PRIVILEGED pod on a node, and a process that exits
			// without deleting them leaves root shells running on the cluster
			// until their one-hour deadline reaps them. The deadline is the
			// backstop; this is the normal path.
			kubernetes.StopAllNodeShells()
			// Local shells next. Nothing in a cluster leaks here — these are
			// processes on this machine — but a shell whose window has gone is
			// a shell nobody can see, type into, or end, and a login shell
			// left behind holds its own children with it. Same rule as the
			// two above: PodSteer started the process, so PodSteer ends it.
			localShells.StopAllLocalShells()
			// Same reason, same place: reflectors are goroutines holding
			// connections, and every one of them has an owner that stops it.
			kubernetes.StopAllWatches()
			historyService.Close()
			// Before Detach, which drops the handle this needs to release
			// what the platform held — a D-Bus connection on Linux. Same rule
			// as the three above: PodSteer opened it.
			desktop.StopNotifications()
			desktop.Detach()
		},
	})

	// The handle every bound service reaches the runtime through. It cannot be
	// a constructor argument: those services are this application's own
	// Services list, so they exist first. See wails.App.Attach.
	desktop.Attach(desktopApp)

	desktopApp.Window.NewWithOptions(wailsapp.WebviewWindowOptions{
		Name:      wailsadapter.MainWindowName,
		Title:     cfg.App.Title,
		Width:     cfg.Window.Width,
		Height:    cfg.Window.Height,
		MinWidth:  cfg.Window.MinWidth,
		MinHeight: cfg.Window.MinHeight,
		URL:       "/",

		// Matches the frontend's dark surface colour, which the splash screen
		// shares regardless of the operator's theme. Without it the webview
		// paints white for the frame or two before the first render, which
		// reads as a flash every launch.
		BackgroundColour: wailsapp.NewRGB(20, 18, 24),

		Mac: wailsapp.MacWindow{
			// An inset title bar lets the UI's own header double as the drag
			// region, which is the native-feeling MD3 layout on macOS.
			TitleBar: wailsapp.MacTitleBarHiddenInset,
			// No appearance pin: the frontend offers a light/dark toggle and
			// there is no runtime handle to re-pin NSAppearance with it, so
			// the window frame follows the OS instead of contradicting one
			// of the two themes.
		},
	})

	// v2's OnStartup hook, in the shape v3 offers it. The work is the same and
	// so is the timing: after the platform has finished launching, which on
	// macOS is when the window and its traffic lights actually exist.
	desktopApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*wailsapp.ApplicationEvent) {
		// No-op on every platform but macOS. See trafficLightVerticalNudge.
		macwindow.NudgeTrafficLights(trafficLightVerticalNudge)
		// Sampling is bounded by the window's own lifetime: it starts when
		// the application does and stops when it closes, which is exactly
		// the window the recorded history claims to cover.
		historyService.Start(desktopApp.Context())
		// It asks for no permission here — see App.StartNotifications — and
		// failing is not fatal.
		desktop.StartNotifications()
	})

	if err := desktopApp.Run(); err != nil {
		return fmt.Errorf("running application: %w", err)
	}

	return nil
}

// newLogger builds the application logger.
//
// Output goes to stderr because stdout belongs to the webview's own plumbing
// in some Wails configurations, and interleaving the two makes both unreadable.
func newLogger(cfg config.LogConfig) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}))
}

// kubeconfigLabel describes the configured kubeconfig for the startup log.
func kubeconfigLabel(path string) string {
	if path == "" {
		return "default ($KUBECONFIG or ~/.kube/config)"
	}
	return path
}
