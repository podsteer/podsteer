package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// fakeVendorCLIs stands in for the runner. It records what it was asked to
// write, which is how these tests check that a CHOSEN id resolves to the
// cluster the CLI itself printed rather than to anything the caller supplied.
type fakeVendorCLIs struct {
	mu       sync.Mutex
	clusters []domain.VendorCluster
	wrote    []domain.VendorCluster
	err      error
}

func (f *fakeVendorCLIs) Providers() []domain.VendorCLIStatus { return nil }

func (f *fakeVendorCLIs) ListClusters(context.Context, string) (domain.VendorClusterList, error) {
	if f.err != nil {
		return domain.VendorClusterList{}, f.err
	}
	return domain.VendorClusterList{Status: domain.VendorListed, Clusters: f.clusters}, nil
}

func (f *fakeVendorCLIs) WriteKubeconfig(_ context.Context, _ string, cluster domain.VendorCluster) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wrote = append(f.wrote, cluster)
	return "kubeconfig for " + cluster.Name, nil
}

func (f *fakeVendorCLIs) Cancel(string) {}

func vendorService(t *testing.T, clis *fakeVendorCLIs) *application.VendorCLIService {
	t.Helper()

	service, err := application.NewVendorCLIService(application.VendorCLIServiceDeps{CLIs: clis})
	if err != nil {
		t.Fatalf("NewVendorCLIService() = %v", err)
	}
	return service
}

func TestAListingIssuesAnIdForEveryCluster(t *testing.T) {
	t.Parallel()

	clis := &fakeVendorCLIs{clusters: []domain.VendorCluster{{Name: "alpha"}, {Name: "beta"}}}
	list, err := vendorService(t, clis).ListClusters(context.Background(), "p")
	if err != nil {
		t.Fatalf("ListClusters() = %v", err)
	}

	for _, cluster := range list.Clusters {
		if cluster.Selection == "" {
			t.Errorf("cluster %q was offered with no id to choose it by", cluster.Name)
		}
	}
	if list.Clusters[0].Selection == list.Clusters[1].Selection {
		t.Error("two clusters share an id")
	}
}

// THE POINT OF THE WHOLE MECHANISM. The frontend sends back an id, and the id
// resolves to the cluster the CLI printed — so a name never makes the round
// trip and cannot come back changed.
func TestAChosenIdResolvesToWhatTheCLIPrinted(t *testing.T) {
	t.Parallel()

	clis := &fakeVendorCLIs{clusters: []domain.VendorCluster{
		{Name: "alpha", Params: map[string]string{"resourceGroup": "rg-1"}},
	}}
	service := vendorService(t, clis)

	list, _ := service.ListClusters(context.Background(), "p")
	text, err := service.KubeconfigFor(context.Background(), "p", list.Clusters[0].Selection)
	if err != nil {
		t.Fatalf("KubeconfigFor() = %v", err)
	}

	if !strings.Contains(text, "alpha") {
		t.Errorf("text = %q, want the chosen cluster's", text)
	}
	if got := clis.wrote[0]; got.Name != "alpha" || got.Params["resourceGroup"] != "rg-1" {
		t.Errorf("wrote %+v, want the cluster exactly as the CLI printed it", got)
	}
}

func TestAnIdNobodyIssuedIsRefused(t *testing.T) {
	t.Parallel()

	clis := &fakeVendorCLIs{clusters: []domain.VendorCluster{{Name: "alpha"}}}
	service := vendorService(t, clis)
	_, _ = service.ListClusters(context.Background(), "p")

	_, err := service.KubeconfigFor(context.Background(), "p", "p:1:99")
	if !errors.Is(err, domain.ErrVendorUnknown) {
		t.Fatalf("error = %v, want a refusal", err)
	}
	if len(clis.wrote) != 0 {
		t.Error("something was written for a cluster nobody offered")
	}
}

// An id from a listing since replaced is refused. A cluster deleted between
// two listings must not stay choosable because somebody left a dialog open.
func TestAnIdFromASupersededListingIsRefused(t *testing.T) {
	t.Parallel()

	clis := &fakeVendorCLIs{clusters: []domain.VendorCluster{{Name: "alpha"}}}
	service := vendorService(t, clis)

	first, _ := service.ListClusters(context.Background(), "p")
	stale := first.Clusters[0].Selection

	clis.clusters = []domain.VendorCluster{{Name: "beta"}}
	if _, err := service.ListClusters(context.Background(), "p"); err != nil {
		t.Fatalf("second ListClusters() = %v", err)
	}

	if _, err := service.KubeconfigFor(context.Background(), "p", stale); !errors.Is(err, domain.ErrVendorUnknown) {
		t.Fatalf("error = %v, want the stale id refused", err)
	}
}

// Each provider keeps its own listing: choosing in one must not resolve
// against the other's.
func TestOneProvidersIdIsNotAnothers(t *testing.T) {
	t.Parallel()

	clis := &fakeVendorCLIs{clusters: []domain.VendorCluster{{Name: "alpha"}}}
	service := vendorService(t, clis)

	list, _ := service.ListClusters(context.Background(), "first")
	if _, err := service.KubeconfigFor(context.Background(), "second", list.Clusters[0].Selection); err == nil {
		t.Fatal("an id issued for one provider was redeemed against another")
	}
}
