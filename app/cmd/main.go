// Package cmd is the PodSteer composition root.
//
// This is the one place that knows every layer: it reads configuration, builds
// the concrete adapters, injects them into the use cases and hands the result
// to Wails. Nothing else in the codebase constructs a dependency, which is
// what keeps the arrows in the hexagon pointing inward.
//
// # Why this is not package main
//
// The Wails CLI compiles the package in the project root — `wails build` runs
// `go build` with its working directory set there and no package argument — so
// the `main` package has to live at the repository root. That root main.go is
// a three-line shim that calls Main below; every line of real wiring is here,
// under app/, where the project layout requires it.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	wailsapp "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/podsteer/podsteer/app/adapters/assets"
	historystore "github.com/podsteer/podsteer/app/adapters/history"
	"github.com/podsteer/podsteer/app/adapters/k8s"
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
	if err := run(); err != nil {
		// The logger may not exist yet if configuration itself failed, so this
		// deliberately writes to stderr directly.
		fmt.Fprintf(os.Stderr, "podsteer: %v\n", err)
		os.Exit(1)
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
	})
	if err != nil {
		return fmt.Errorf("wiring management service: %w", err)
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

	managementAPI, err := wailsadapter.NewManagementAPI(managementService, kubernetes, workloadService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring management API: %w", err)
	}

	terminalAPI, err := wailsadapter.NewTerminalAPI(managementService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring terminal API: %w", err)
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

	frontend, err := assets.FS()
	if err != nil {
		return err
	}

	// --- Window ------------------------------------------------------------

	err = wailsapp.Run(&options.App{
		Title:     cfg.App.Title,
		Width:     cfg.Window.Width,
		Height:    cfg.Window.Height,
		MinWidth:  cfg.Window.MinWidth,
		MinHeight: cfg.Window.MinHeight,

		AssetServer: &assetserver.Options{Assets: frontend},

		// Matches the frontend's dark surface colour, which the splash screen
		// shares regardless of the operator's theme. Without it the webview
		// paints white for the frame or two before the first render, which
		// reads as a flash every launch.
		BackgroundColour: &options.RGBA{R: 20, G: 18, B: 24, A: 1},

		OnStartup: func(ctx context.Context) {
			desktop.OnStartup(ctx)
			// No-op on every platform but macOS. See trafficLightVerticalNudge.
			macwindow.NudgeTrafficLights(trafficLightVerticalNudge)
			// Sampling is bounded by the window's own lifetime: it starts when
			// the application does and stops when it closes, which is exactly
			// the window the recorded history claims to cover.
			historyService.Start(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			// Forwards first: each one holds a local socket, and a process
			// that exits without releasing them leaves ports bound until the
			// operating system reaps them. That is the orphaned-port
			// complaint every competing client has an issue open about, and
			// the fix is to close them rather than to hope.
			kubernetes.StopAllPortForwards()
			// Same reason, same place: reflectors are goroutines holding
			// connections, and every one of them has an owner that stops it.
			kubernetes.StopAllWatches()
			historyService.Close()
			desktop.OnShutdown(ctx)
		},

		// Everything bound here becomes callable from TypeScript, and Wails
		// generates the declarations for it into web/src/lib/wailsjs.
		Bind: []any{
			clusterAPI,
			workloadAPI,
			browseAPI,
			overviewAPI,
			fleetAPI,
			historyAPI,
			managementAPI,
			terminalAPI,
			systemAPI,
			updateAPI,
		},

		// Only one PodSteer should hold the kubeconfig and its client caches;
		// a second launch raises the existing window instead.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.podsteer.desktop",
		},

		Mac: &mac.Options{
			// An inset title bar lets the UI's own header double as the drag
			// region, which is the native-feeling MD3 layout on macOS.
			TitleBar: mac.TitleBarHiddenInset(),
			// No appearance pin: the frontend offers a light/dark toggle and
			// there is no runtime handle to re-pin NSAppearance with it, so
			// the window frame follows the OS instead of contradicting one
			// of the two themes.
			About: &mac.AboutInfo{
				Title:   cfg.App.Title,
				Message: "A fast, native Kubernetes client.\nVersion " + cfg.App.Version,
			},
		},
	})
	if err != nil {
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
