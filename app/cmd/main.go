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
	"fmt"
	"log/slog"
	"os"

	wailsapp "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"k8sense/app/adapters/assets"
	"k8sense/app/adapters/k8s"
	wailsadapter "k8sense/app/adapters/wails"
	"k8sense/app/application"
	"k8sense/app/config"
)

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

	session := application.NewSession()

	clusterService, err := application.NewClusterService(application.ClusterServiceDeps{
		Kubeconfig: kubernetes,
		Kubernetes: kubernetes,
		Events:     desktop,
		Session:    session,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("wiring cluster service: %w", err)
	}

	workloadService, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Kubernetes: kubernetes,
		Session:    session,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("wiring workload service: %w", err)
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

		// Matches the frontend's dark surface colour. Without it the webview
		// paints white for the frame or two before the first render, which
		// reads as a flash every launch.
		BackgroundColour: &options.RGBA{R: 20, G: 18, B: 24, A: 1},

		OnStartup:  desktop.OnStartup,
		OnShutdown: desktop.OnShutdown,

		// Everything bound here becomes callable from TypeScript, and Wails
		// generates the declarations for it into web/src/lib/wailsjs.
		Bind: []any{
			clusterAPI,
			workloadAPI,
		},

		// Only one K8Sense should hold the kubeconfig and its client caches;
		// a second launch raises the existing window instead.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.k8sense.desktop",
		},

		Mac: &mac.Options{
			// An inset title bar lets the UI's own header double as the drag
			// region, which is the native-feeling MD3 layout on macOS.
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
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
