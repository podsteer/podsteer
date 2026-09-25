package wails

import (
	"errors"
	"log/slog"

	"github.com/podsteer/podsteer/app/ports"
)

// VendorCLIAPI offers the clusters a cloud CLI the operator has can see.
//
// It hands back an OPAQUE ID per cluster and takes one back — never a name.
// See application.VendorCLIService for why, and decision 12 for what this is
// allowed to do at all.
type VendorCLIAPI struct {
	clis   ports.VendorCLIService
	app    *App
	logger *slog.Logger
}

// VendorProvider is one cloud CLI and whether it is on this machine.
type VendorProvider struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Binary is what was looked for, so a pane can say which program is
	// missing rather than only that one is.
	Binary string `json:"binary"`
	// Path is where it was found — shown so an operator can see WHICH one
	// PodSteer would run when several are installed.
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	// SignInHint is one sentence shown beside the CLI's own words, never
	// instead of them.
	SignInHint string `json:"signInHint"`
}

// VendorCluster is one cluster a CLI reported.
type VendorCluster struct {
	Name string `json:"name"`
	// Params are whatever else that provider needs to name it again — a
	// resource group, a location — shown as a second line so an operator can
	// tell two clusters of the same name apart.
	Params map[string]string `json:"params"`
	// Selection is the id to send back to choose this one.
	Selection string `json:"selection"`
}

// VendorClusterList is one CLI's answer.
type VendorClusterList struct {
	Provider string `json:"provider"`
	// Status is "listed" or "declined". LISTED WITH NOTHING IN IT IS NOT
	// DECLINED: an account with no clusters and a CLI that would not answer
	// need opposite sentences.
	Status   string          `json:"status"`
	Clusters []VendorCluster `json:"clusters"`
	// Reason is the CLI's own words when it declined, verbatim.
	Reason string `json:"reason"`
}

// NewVendorCLIAPI returns the bound API.
func NewVendorCLIAPI(clis ports.VendorCLIService, app *App, logger *slog.Logger) (*VendorCLIAPI, error) {
	switch {
	case clis == nil:
		return nil, errors.New("wails: VendorCLIAPI requires a VendorCLIService")
	case app == nil:
		return nil, errors.New("wails: VendorCLIAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}
	return &VendorCLIAPI{clis: clis, app: app, logger: logger.With(slog.String("api", "vendorcli"))}, nil
}

// Providers reports the cloud CLIs PodSteer can drive and whether each is here.
//
// A PATH LOOKUP AND NOTHING ELSE. Opening the Add cluster dialog must not run
// anybody's cloud CLI.
func (v *VendorCLIAPI) Providers() ([]VendorProvider, error) {
	ctx, cancel := v.app.requestContext()
	defer cancel()

	found := v.clis.Providers(ctx)
	out := make([]VendorProvider, 0, len(found))
	for _, status := range found {
		out = append(out, VendorProvider{
			ID:         status.ID,
			Label:      status.Label,
			Binary:     status.Binary,
			Path:       status.Path,
			Installed:  status.Installed,
			SignInHint: status.SignInHint,
		})
	}
	return out, nil
}

// ListClusters asks one CLI which clusters it can see.
func (v *VendorCLIAPI) ListClusters(provider string) (VendorClusterList, error) {
	ctx, cancel := v.app.requestContext()
	defer cancel()

	list, err := v.clis.ListClusters(ctx, provider)
	if err != nil {
		return VendorClusterList{}, apiError(v.logger, "ListClusters", err)
	}

	clusters := make([]VendorCluster, 0, len(list.Clusters))
	for _, cluster := range list.Clusters {
		clusters = append(clusters, VendorCluster{
			Name:      cluster.Name,
			Params:    cluster.Params,
			Selection: cluster.Selection,
		})
	}

	return VendorClusterList{
		Provider: list.Provider,
		Status:   string(list.Status),
		Clusters: clusters,
		Reason:   list.Reason,
	}, nil
}

// KubeconfigFor has the CLI write an entry for a chosen cluster and returns the
// text.
//
// THE TEXT, NOT THE WRITE. What comes back goes through PreviewKubeconfig and
// AddKubeconfig exactly as a pasted document does, so the operator's kubeconfig
// is still written in one place, with its backup, its atomic rename and its
// refusal to touch current-context.
func (v *VendorCLIAPI) KubeconfigFor(provider, selection string) (string, error) {
	ctx, cancel := v.app.requestContext()
	defer cancel()

	text, err := v.clis.KubeconfigFor(ctx, provider, selection)
	if err != nil {
		return "", apiError(v.logger, "KubeconfigFor", err)
	}
	return text, nil
}

// Cancel stops a listing that is still running.
func (v *VendorCLIAPI) Cancel(provider string) error {
	ctx, cancel := v.app.requestContext()
	defer cancel()

	if err := v.clis.Cancel(ctx, provider); err != nil {
		return apiError(v.logger, "Cancel", err)
	}
	return nil
}
