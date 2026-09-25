package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// VendorCLIService offers the clusters a cloud CLI can see, and turns one of
// them into a kubeconfig entry.
//
// WHAT IT ADDS OVER THE PORT is the thing that keeps a cluster name from ever
// travelling back from the frontend: it remembers the listing it issued, hands
// out opaque ids, and resolves a chosen id to the cluster the CLI ITSELF
// printed. A selection it never issued is refused. A selection from a listing
// since replaced is refused too, by generation — the guard the Kubernetes
// adapter uses for late writes, for the same reason: an answer from before the
// world changed must not be acted on after it.
type VendorCLIService struct {
	clis   ports.VendorCLIPort
	logger *slog.Logger

	mu sync.Mutex
	// offered holds the last listing per provider, keyed by the id handed to
	// the frontend. IN MEMORY AND NOWHERE ELSE: nothing here is written to
	// settings, to history or to the timeline, and it is gone when the
	// application exits.
	offered map[string]map[string]domain.VendorCluster
	// generation counts listings per provider, so an id from an older one
	// cannot be redeemed against a newer.
	generation map[string]int
}

// VendorCLIServiceDeps are what the service needs.
type VendorCLIServiceDeps struct {
	// CLIs runs them. Required.
	CLIs ports.VendorCLIPort
	// Logger receives diagnostics. Optional.
	Logger *slog.Logger
}

var _ ports.VendorCLIService = (*VendorCLIService)(nil)

// NewVendorCLIService validates deps and returns the service.
func NewVendorCLIService(deps VendorCLIServiceDeps) (*VendorCLIService, error) {
	if deps.CLIs == nil {
		return nil, errors.New("application: VendorCLIService requires a VendorCLIPort")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &VendorCLIService{
		clis:       deps.CLIs,
		logger:     logger.With(slog.String("service", "vendorcli")),
		offered:    make(map[string]map[string]domain.VendorCluster, 2),
		generation: make(map[string]int, 2),
	}, nil
}

// Providers reports the CLIs in the table and whether each is installed.
func (s *VendorCLIService) Providers(context.Context) []domain.VendorCLIStatus {
	return s.clis.Providers()
}

// ListClusters asks one CLI, and remembers what it said.
func (s *VendorCLIService) ListClusters(ctx context.Context, provider string) (domain.VendorClusterList, error) {
	list, err := s.clis.ListClusters(ctx, provider)
	if err != nil {
		return domain.VendorClusterList{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.generation[provider]++
	offered := make(map[string]domain.VendorCluster, len(list.Clusters))
	for index := range list.Clusters {
		id := fmt.Sprintf("%s:%d:%d", provider, s.generation[provider], index)
		offered[id] = list.Clusters[index]
		list.Clusters[index].Selection = id
	}
	// REPLACED, NOT MERGED. The previous listing's ids stop working the moment
	// a new one lands, which is the point: a cluster that has since been
	// deleted must not stay choosable because somebody left a dialog open.
	s.offered[provider] = offered

	return list, nil
}

// KubeconfigFor writes an entry for a cluster the last listing issued.
func (s *VendorCLIService) KubeconfigFor(ctx context.Context, provider, selection string) (string, error) {
	s.mu.Lock()
	cluster, ok := s.offered[provider][selection]
	s.mu.Unlock()

	if !ok {
		// NOT A VALIDATION FAILURE, AN UNKNOWN ONE. There is no cluster name
		// to check here because none was sent: what arrived is an id this
		// service either issued or did not.
		return "", fmt.Errorf("%w: that cluster is not one PodSteer offered", domain.ErrVendorUnknown)
	}

	text, err := s.clis.WriteKubeconfig(ctx, provider, cluster)
	if err != nil {
		return "", err
	}
	return text, nil
}

// Cancel stops a run in the air.
func (s *VendorCLIService) Cancel(_ context.Context, provider string) error {
	s.clis.Cancel(provider)
	return nil
}
