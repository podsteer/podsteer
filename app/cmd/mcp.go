package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/podsteer/podsteer/app/adapters/k8s"
	"github.com/podsteer/podsteer/app/adapters/mcp"
	"github.com/podsteer/podsteer/app/adapters/shellpath"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/config"
	"github.com/podsteer/podsteer/app/domain"
)

// mcpUsage is printed for `podsteer mcp --help`.
//
// Short on purpose: the audience is whoever is pasting a command into an
// agent's configuration, and everything else they need is in the README.
const mcpUsage = `podsteer mcp — serve PodSteer's read-only Kubernetes tools to a
coding agent over the Model Context Protocol, on stdin and stdout.

It opens no port, serves nothing over HTTP and contacts nothing PodSteer
operates. It reads the clusters in your own kubeconfig, with your own
credentials and permissions, and it cannot write to any of them.

Configure your agent to run this command; it is not started by hand.
`

// runMCP serves the Model Context Protocol on stdio until the stream ends.
//
// A SECOND COMPOSITION ROOT, deliberately small, rather than a flag inside
// run(). Nothing about the window applies here — no assets, no bindings, no
// sampler writing history, no single-instance lock (an agent may well start
// this while the application is open, and it must not raise somebody's window
// or refuse to start) — so sharing the wiring would mean a run() full of
// branches for a mode that needs almost none of it.
func runMCP(args []string) error {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			fmt.Print(mcpUsage)
			return nil
		default:
			// Never ignored: an unrecognised flag usually means somebody
			// expected an option this build does not have, and carrying on
			// would serve them something other than what they asked for.
			return fmt.Errorf("mcp: unknown argument %q", arg)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// STDOUT IS THE TRANSPORT. Every log line goes to stderr — newLogger
	// already does that for the window's own reasons — and one stray byte on
	// stdout is a parse error the agent reports as a broken server.
	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)

	// The same PATH problem the window has, for the same reason and with more
	// force: an agent launched from a desktop application inherits launchd's
	// environment, so `aws eks get-token` and its equivalents are not on the
	// PATH this process starts with. Resolved alongside the serve loop, and
	// waited on only when the first client is built — see k8s.Config.EnvReady.
	envReady := make(chan struct{})
	go func() {
		defer close(envReady)
		logger.Debug("resolved PATH", slog.String("result", shellpath.Resolve(context.Background())))
	}()

	// READ-ONLY, and that flag is how SECURITY.md's "nothing is written
	// anywhere" stays literally true for this subcommand. The store reads the
	// operator's kubeconfig sources so an agent sees the same clusters the
	// window does, and it creates no directory, writes no file and adopts
	// nothing — Update returns ports.ErrSettingsReadOnly before it looks at
	// anything. main_test.go asserts the directory is byte-identical after
	// this composition has run.
	settingsStore := openSettings(true, logger)

	kubernetes := k8s.New(k8s.Config{
		KubeconfigPath: cfg.Kubernetes.KubeconfigPath,
		KubeconfigDir:  cfg.Kubernetes.KubeconfigDir,
		Sources:        kubeconfigSources(settingsStore),
		QPS:            cfg.Kubernetes.QPS,
		Burst:          cfg.Kubernetes.Burst,
		// Named apart from the window's own agent string so an operator
		// reading their API server's audit log can tell a question their
		// coding agent asked from a pane they had open.
		UserAgent: fmt.Sprintf("%s-mcp/%s", cfg.App.Name, cfg.App.Version),
		EnvReady:  envReady,
		// NO WATCH. A mirror pays for itself under a UI that re-reads the
		// same lists every few seconds; an agent asks a handful of questions
		// minutes apart, so a reflector here would list and then stream a
		// whole cluster to answer one question about one namespace.
		LiveWatch: false,
	}, logger)
	// Nothing above starts a stream, but the adapter's own machinery may, and
	// a goroutine holding a connection is stopped by whoever owns it.
	defer kubernetes.StopAllWatches()

	registry := application.NewRegistry()
	catalog := domain.NewCatalog()

	clusterService, err := application.NewClusterService(application.ClusterServiceDeps{
		Kubeconfig:  kubernetes,
		Cluster:     kubernetes,
		Workloads:   kubernetes,
		Metrics:     kubernetes,
		Events:      silentPublisher{},
		Registry:    registry,
		Catalog:     catalog,
		Logger:      logger,
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

	rbacService, err := application.NewRBACService(application.RBACServiceDeps{
		RBAC:     kubernetes,
		Registry: registry,
		Logger:   logger,
	})
	if err != nil {
		return fmt.Errorf("wiring rbac service: %w", err)
	}

	// Wired for its ONE read: StreamLogs. Its writes are unreachable from the
	// server, which accepts a LogReader rather than this whole service — the
	// narrowing is at that boundary rather than here, so it holds however
	// this composition is later rearranged. No archive is wired: a file copy
	// is a write on one side and a local disk write on the other, and neither
	// is on offer.
	managementService, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: kubernetes,
		Registry:   registry,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("wiring management service: %w", err)
	}

	server, err := mcp.New(mcp.Deps{
		Clusters:  clusterService,
		Kinds:     browseService,
		Workloads: workloadService,
		Events:    browseService,
		Resources: browseService,
		Overview:  overviewService,
		RBAC:      rbacService,
		Logs:      managementService,
		Version:   cfg.App.Version,
		Timeout:   cfg.Kubernetes.RequestTimeout,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("wiring mcp server: %w", err)
	}

	// An agent normally ends this by closing the pipe, which Serve reads as
	// end of input. The signals are for the operator who started it by hand.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("serving mcp on stdio",
		slog.String("version", cfg.App.Version),
		slog.Int("tools", len(server.Tools())))

	return server.Serve(ctx, os.Stdin, os.Stdout)
}

// silentPublisher discards the connection lifecycle events the window turns
// into tab state.
//
// There is no window here and nothing observing, so the alternative is a
// nil check inside ClusterService — a required dependency that is sometimes
// absent, which is worse than one implementation that does nothing.
type silentPublisher struct{}

func (silentPublisher) Publish(context.Context, domain.DomainEvent) {}
