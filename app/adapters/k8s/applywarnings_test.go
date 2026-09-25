package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestWarningCollectorKeepsTheTextAndDropsTheRest(t *testing.T) {
	t.Parallel()

	collector := &warningCollector{}
	// The code is always 299 in practice and the agent is the API server's
	// own name. Neither tells an operator anything the text does not.
	collector.HandleWarningHeader(299, "kube-apiserver", "apps/v1beta1 Deployment is deprecated")
	collector.HandleWarningHeader(299, "kube-apiserver", "  spaced out  ")
	collector.HandleWarningHeader(299, "kube-apiserver", "   ")

	got := collector.collected()
	want := []string{"apps/v1beta1 Deployment is deprecated", "spaced out"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("collected() = %v, want %v", got, want)
	}
}

func TestWarningCollectorDeduplicates(t *testing.T) {
	// One apply is up to two requests — a dry run and then the real write —
	// and a server that warns about a deprecated apiVersion warns on both. An
	// operator reading the same sentence twice would reasonably wonder what
	// the second one was about.
	t.Parallel()

	collector := &warningCollector{}
	for range 3 {
		collector.HandleWarningHeader(299, "kube-apiserver", "this kind is deprecated")
	}

	if got := collector.collected(); len(got) != 1 {
		t.Fatalf("collected() = %v, want one entry", got)
	}
}

func TestWarningCollectorIsEmptyRatherThanEmptySlice(t *testing.T) {
	// ApplyOutcome.Warnings documents empty as the common case and not worth
	// reporting as an absence; the DTO turns nil into [] for the wire.
	t.Parallel()

	if got := (&warningCollector{}).collected(); got != nil {
		t.Fatalf("collected() = %v, want nil", got)
	}
}

// TestApplyCollectsTheWarningsTheServerSends is the one that proves the
// WIRING, against a real client and a real HTTP response.
//
// The collector being correct is worth little on its own: the defect this
// closes was that nothing ever installed it. client-go hangs the handler off
// the REST config rather than the request, so the question is whether a
// per-apply config copy actually reaches the response headers — and only a
// real rest.Config talking to a real server answers that.
func TestApplyCollectsTheWarningsTheServerSends(t *testing.T) {
	t.Parallel()

	const deprecation = "apps/v1beta1 Deployment is deprecated in v1.25+, use apps/v1 Deployment"

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Two warnings, one of them repeated, exactly as an API server that
		// warns on both the dry run and the write would produce.
		writer.Header().Add("Warning", `299 - "`+deprecation+`"`)
		writer.Header().Add("Warning", `299 - "`+deprecation+`"`)
		writer.Header().Add("Warning", `299 - "would violate PodSecurity restricted:latest"`)
		writer.Header().Set("Content-Type", "application/json")

		object := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "web", "namespace": "shop"},
		}}
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(object)
	}))
	defer server.Close()

	set := &clients{config: &rest.Config{Host: server.URL}}
	client, collector, err := applyClient(set)
	if err != nil {
		t.Fatalf("applyClient() error = %v", err)
	}

	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "shop"},
	}}

	if _, err := client.Resource(gvr).Namespace("shop").
		Create(context.Background(), object, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got := collector.collected()
	if len(got) != 2 {
		t.Fatalf("collected() = %v, want two distinct warnings", got)
	}
	if got[0] != deprecation {
		t.Errorf("first warning = %q, want the deprecation verbatim", got[0])
	}
	if !strings.Contains(got[1], "PodSecurity") {
		t.Errorf("second warning = %q, want the admission warning", got[1])
	}
}

// TestTwoAppliesDoNotSeeEachOthersWarnings is the reason this is per apply.
//
// client-go configures the handler on the REST CONFIG, which one cluster's
// clients share. A collector installed there would hand whichever apply read
// the slice next somebody else's warnings — two tabs on the same context are
// enough. Each apply gets its own config copy, so each gets its own.
func TestTwoAppliesDoNotSeeEachOthersWarnings(t *testing.T) {
	t.Parallel()

	set := &clients{config: &rest.Config{Host: "https://example.invalid"}}

	_, first, err := applyClient(set)
	if err != nil {
		t.Fatalf("applyClient() error = %v", err)
	}
	_, second, err := applyClient(set)
	if err != nil {
		t.Fatalf("applyClient() error = %v", err)
	}

	first.HandleWarningHeader(299, "kube-apiserver", "the first apply's warning")

	if got := second.collected(); got != nil {
		t.Fatalf("the second apply collected %v — the collectors are shared", got)
	}
	if got := first.collected(); len(got) != 1 {
		t.Fatalf("the first apply collected %v, want its own warning", got)
	}
}

// TestAClientSetWithNoConfigCollectsNothingRatherThanPanicking pins the
// fallback, which exists because tests assemble a clients around a fake
// dynamic client and never give it a REST config.
func TestAClientSetWithNoConfigCollectsNothingRatherThanPanicking(t *testing.T) {
	t.Parallel()

	client, collector, err := applyClient(&clients{})
	if err != nil {
		t.Fatalf("applyClient() error = %v", err)
	}
	if client != nil {
		t.Error("a set with no dynamic client returned one")
	}
	if got := collector.collected(); got != nil {
		t.Fatalf("collected() = %v, want nothing", got)
	}
}
