package k8s

import (
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

// listingAdapter returns an adapter over a fake clientset holding pods, with
// the watch disabled so every read takes the network path, and a counter of
// how many pod LISTs left.
func listingAdapter(t *testing.T, pods ...*corev1.Pod) (*Adapter, *atomic.Int64) {
	t.Helper()

	objects := make([]runtime.Object, 0, len(pods))
	for _, pod := range pods {
		objects = append(objects, pod)
	}
	client := fake.NewSimpleClientset(objects...)

	var lists atomic.Int64
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})

	adapter := newTestAdapter("dev", client)
	adapter.watches = newWatchManager(false, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, recheckEvery)
	return adapter, &lists
}

func annotatedPod(name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "web",
		Labels:    map[string]string{"app": name},
		Annotations: map[string]string{
			"team":                             "payments",
			"owner":                            "alice",
			corev1.LastAppliedConfigAnnotation: `{"whole":"manifest"}`,
		},
	}}
}

func TestListPodsCarriesOnlyTheProjectedAnnotations(t *testing.T) {
	adapter, _ := listingAdapter(t, annotatedPod("api-1"))

	pods, err := adapter.ListPods(t.Context(), "dev", domain.NamespaceAll, domain.NewProjection([]string{"team"}))
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("ListPods() = %d pods, want 1", len(pods))
	}
	if got, want := pods[0].Annotations(), map[string]string{"team": "payments"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Annotations() = %v, want %v", got, want)
	}
	if got, want := pods[0].Labels(), map[string]string{"app": "api-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Labels() = %v, want %v", got, want)
	}
}

func TestAProjectedReadIsKeyedApartFromAnUnprojectedOne(t *testing.T) {
	// THE PROJECTION IS PART OF THE READ. The cache holds mapped domain
	// values, which carry only what they were asked for, so an unprojected
	// read in flight cannot answer a projected one — handing it over would
	// blank the column on exactly the tick the assessment happened to arrive
	// first. Two projections, two lists; the same projection twice, one.
	adapter, lists := listingAdapter(t, annotatedPod("api-1"))
	ctx := t.Context()

	if _, err := adapter.ListPods(ctx, "dev", domain.NamespaceAll, domain.Projection{}); err != nil {
		t.Fatalf("unprojected ListPods() error = %v", err)
	}
	projected, err := adapter.ListPods(ctx, "dev", domain.NamespaceAll, domain.NewProjection([]string{"team"}))
	if err != nil {
		t.Fatalf("projected ListPods() error = %v", err)
	}
	if projected[0].Annotations()["team"] != "payments" {
		t.Fatalf("the projected read was served an unprojected pod: %v", projected[0].Annotations())
	}
	if got := lists.Load(); got != 2 {
		t.Fatalf("two different projections cost %d lists, want 2", got)
	}

	// The same projection named in a different order is the same read, and
	// within the cache window it costs nothing.
	if _, err := adapter.ListPods(ctx, "dev", domain.NamespaceAll, domain.NewProjection([]string{"team", "team"})); err != nil {
		t.Fatalf("repeated ListPods() error = %v", err)
	}
	if got := lists.Load(); got != 2 {
		t.Fatalf("repeating a projection cost another list: %d, want 2", got)
	}
}
