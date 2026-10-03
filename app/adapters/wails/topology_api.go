package wails

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// topologyChangedEvent is emitted when something in a drawn topology scope
// changed. The page says "Changed — Refresh", or redraws if Live is on; it
// is never a redraw by itself.
const topologyChangedEvent = "topology:changed"

// maxTopologyPNG bounds an exported image. A canvas the webview can draw is
// far smaller; anything larger is not a picture of a map.
const maxTopologyPNG = 64 << 20

// pngSignature is the eight bytes every PNG starts with.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

var (
	errNotPNG      = errors.New("the image is not a PNG")
	errPNGTooLarge = fmt.Errorf("the image is larger than %d MB", maxTopologyPNG>>20)
)

// TopologyAPI exposes the namespace topology to the frontend.
type TopologyAPI struct {
	topology ports.TopologyService
	app      *App
	logger   *slog.Logger

	// chooseSavePath is the save dialog behind ExportTopologyPNG — a seam for
	// the reason SystemAPI.chooseSavePath is one.
	chooseSavePath func(suggestedName string) (string, error)

	mu          sync.Mutex
	unsubscribe func()
}

// NewTopologyAPI returns the bound topology API.
func NewTopologyAPI(topology ports.TopologyService, app *App, logger *slog.Logger) (*TopologyAPI, error) {
	switch {
	case topology == nil:
		return nil, errors.New("wails: TopologyAPI requires a TopologyService")
	case app == nil:
		return nil, errors.New("wails: TopologyAPI requires an App")
	}
	if logger == nil {
		logger = slog.Default()
	}
	t := &TopologyAPI{
		topology: topology,
		app:      app,
		logger:   logger.With(slog.String("api", "topology")),
	}
	t.chooseSavePath = func(suggestedName string) (string, error) { return showSaveDialog(app, suggestedName) }
	return t, nil
}

// ServiceStartup subscribes to the change feed and forwards each coalesced
// change as a `topology:changed` event. Called by Wails, not by the page.
func (t *TopologyAPI) ServiceStartup(context.Context, application.ServiceOptions) error {
	cancel := t.topology.Subscribe(func(change domain.ClusterChange) {
		t.app.emit(topologyChangedEvent, toTopologyChanged(change))
	})
	t.mu.Lock()
	t.unsubscribe = cancel
	t.mu.Unlock()
	return nil
}

// ServiceShutdown stops forwarding changes. Called by Wails.
func (t *TopologyAPI) ServiceShutdown() error {
	t.mu.Lock()
	cancel := t.unsubscribe
	t.unsubscribe = nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Topology draws the namespaces of one cluster, or all of them. Drawing a
// scope also subscribes it to `topology:changed`.
func (t *TopologyAPI) Topology(clusterID string, namespaces []string, all bool) (TopologyGraph, error) {
	ctx, cancel := t.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return TopologyGraph{}, apiError(t.logger, "Topology", err)
	}
	scope, err := domain.NewTopologyScope(namespaces, all)
	if err != nil {
		return TopologyGraph{}, apiError(t.logger, "Topology", err)
	}

	graph, err := t.topology.Topology(ctx, id, scope)
	if err != nil {
		return TopologyGraph{}, apiError(t.logger, "Topology", err)
	}
	return toTopologyGraph(graph), nil
}

// Release stops `topology:changed` for a cluster, for when its topology page
// closes. Optional: a drawn scope also expires on its own.
func (t *TopologyAPI) Release(clusterID string) error {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(t.logger, "Release", err)
	}
	t.topology.Release(id)
	return nil
}

// ExportTopologyPNG writes a PNG the page rendered to wherever the operator
// picks in a save dialog, and returns the path — "" when they cancelled.
//
// The image arrives as base64 (a `data:image/png;base64,` prefix is
// accepted) and is checked to BE a PNG before anything is written: the page
// cannot touch the filesystem, and this is not a general file writer.
func (t *TopologyAPI) ExportTopologyPNG(suggestedName string, pngBase64 string) (string, error) {
	if strings.TrimSpace(suggestedName) == "" {
		return "", apiError(t.logger, "ExportTopologyPNG", errEmptySuggestedName)
	}
	image, err := decodePNG(pngBase64)
	if err != nil {
		return "", apiError(t.logger, "ExportTopologyPNG", err)
	}

	path, err := t.chooseSavePath(suggestedName)
	if err != nil {
		return "", apiError(t.logger, "ExportTopologyPNG", err)
	}
	if path == "" {
		return "", nil
	}
	// 0o600, as every operator-placed write: a map names a cluster's objects.
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return "", apiError(t.logger, "ExportTopologyPNG", err)
	}
	return path, nil
}

// decodePNG decodes and checks an exported image.
func decodePNG(encoded string) ([]byte, error) {
	encoded = strings.TrimPrefix(strings.TrimSpace(encoded), "data:image/png;base64,")
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxTopologyPNG {
		return nil, errPNGTooLarge
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNotPNG, err)
	}
	if !bytes.HasPrefix(image, pngSignature) {
		return nil, errNotPNG
	}
	return image, nil
}
