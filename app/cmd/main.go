// Package cmd is the K8Sense composition root.
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

	wailsapp "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"k8sense/app/adapters/assets"
	"k8sense/app/adapters/k8s"
	"k8sense/app/adapters/macwindow"
	wailsadapter "k8sense/app/adapters/wails"
	"k8sense/app/application"
	"k8sense/app/config"
	"k8sense/app/domain"
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

// Main starts K8Sense and terminates the process on failure.
//
// It is the only function in the codebase that calls os.Exit, so every other
// layer stays testable and composable.
func Main() {
	if err := run(); err != nil {
		// The logger may not exist yet if configuration itself failed, so this
		// deliberately writes to stderr directly.
		fmt.Fprintf(os.Stderr, "k8sense: %v\n", err)
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

	logger.Info("starting k8sense",
		slog.String("version", cfg.App.Version),
		slog.String("kubeconfig", kubeconfigLabel(cfg.Kubernetes.KubeconfigPath)))

	// --- Driven (outbound) adapters -------------------------------------
	//
	// Built first because everything inward depends on them. The Kubernetes
	// adapter performs no I/O here: a machine with an unreachable cluster, or
	// none at all, still reaches a usable window.

	kubernetes := k8s.New(k8s.Config{
		KubeconfigPath: cfg.Kubernetes.KubeconfigPath,
		QPS:            cfg.Kubernetes.QPS,
		Burst:          cfg.Kubernetes.Burst,
		UserAgent:      fmt.Sprintf("%s/%s", cfg.App.Name, cfg.App.Version),
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
		Metrics:    kubernetes,
		Events:     desktop,
		Registry:   registry,
		Catalog:    catalog,
		Logger:     logger,
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

	managementService, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: kubernetes,
		Logger:     logger,
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

	managementAPI, err := wailsadapter.NewManagementAPI(managementService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring management API: %w", err)
	}

	terminalAPI, err := wailsadapter.NewTerminalAPI(managementService, desktop, logger)
	if err != nil {
		return fmt.Errorf("wiring terminal API: %w", err)
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
		},
		OnShutdown: desktop.OnShutdown,

		// Everything bound here becomes callable from TypeScript, and Wails
		// generates the declarations for it into web/src/lib/wailsjs.
		Bind: []any{
			clusterAPI,
			workloadAPI,
			browseAPI,
			managementAPI,
			terminalAPI,
			systemAPI,
		},

		// Only one K8Sense should hold the kubeconfig and its client caches;
		// a second launch raises the existing window instead.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.k8sense.desktop",
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
