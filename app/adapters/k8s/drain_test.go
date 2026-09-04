package k8s

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// podsGVR is the GroupVersionResource every eviction reactor below matches
// on: the fake routes Evict as a create against "pods" carrying the
// "eviction" subresource, never a resource of its own. See
// EvictionExpansion in client-go's fake package.
var podsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

func testNode(name string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
	}
}

// testNodePod builds a pod scheduled on a node, with the shape DrainCandidates
// reads: an owner reference, a mirror annotation, and an emptyDir volume are
// each added only when the corresponding argument asks for one.
func testNodePod(namespace, name, node string, owner *metav1.OwnerReference, mirror bool, emptyDir bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("uid-" + name),
		},
		Spec: corev1.PodSpec{
			NodeName: node,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if owner != nil {
		pod.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	if mirror {
		pod.Annotations = map[string]string{mirrorPodAnnotation: "uid-of-manifest"}
	}
	if emptyDir {
		pod.Spec.Volumes = []corev1.Volume{{
			Name:         "scratch",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}}
	}
	return pod
}

func replicaSetOwnerRef(name string) *metav1.OwnerReference {
	controller := true
	return &metav1.OwnerReference{Kind: "ReplicaSet", Name: name, Controller: &controller}
}

func daemonSetOwnerRef(name string) *metav1.OwnerReference {
	controller := true
	return &metav1.OwnerReference{Kind: "DaemonSet", Name: name, Controller: &controller}
}

func TestCordonNodeSetsAndClearsUnschedulable(t *testing.T) {
	node := testNode("node-1", false)
	client := fake.NewSimpleClientset(node)
	adapter := newTestAdapter("dev", client)

	if err := adapter.CordonNode(context.Background(), "dev", "node-1", true); err != nil {
		t.Fatalf("CordonNode(true) error = %v", err)
	}
	got, err := client.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting node: %v", err)
	}
	if !got.Spec.Unschedulable {
		t.Fatal("spec.unschedulable is not true after cordoning")
	}

	if err := adapter.CordonNode(context.Background(), "dev", "node-1", false); err != nil {
		t.Fatalf("CordonNode(false) error = %v", err)
	}
	got, err = client.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting node: %v", err)
	}
	if got.Spec.Unschedulable {
		t.Fatal("spec.unschedulable is not false after uncordoning")
	}
}

func TestEvictPodIssuesACreateOnThePodsEvictionSubresource(t *testing.T) {
	pod := testNodePod("default", "web-1", "node-1", replicaSetOwnerRef("web"), false, false)
	client := fake.NewSimpleClientset(pod)

	var recorded []ktesting.Action
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(ktesting.CreateAction)
		if !ok || create.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		recorded = append(recorded, action)
		return true, nil, nil
	})

	adapter := newTestAdapter("dev", client)
	if err := adapter.EvictPod(context.Background(), "dev", "default", "web-1", -1); err != nil {
		t.Fatalf("EvictPod() error = %v", err)
	}

	if len(recorded) != 1 {
		t.Fatalf("recorded actions = %d, want exactly 1 eviction create", len(recorded))
	}
	create := recorded[0].(ktesting.CreateAction)
	if create.GetResource() != podsGVR {
		t.Errorf("resource = %+v, want %+v", create.GetResource(), podsGVR)
	}
	eviction, ok := create.GetObject().(*policyv1.Eviction)
	if !ok {
		t.Fatalf("object = %T, want *policyv1.Eviction", create.GetObject())
	}
	if eviction.Name != "web-1" || eviction.Namespace != "default" {
		t.Errorf("eviction = %s/%s, want default/web-1", eviction.Namespace, eviction.Name)
	}
}

func TestEvictPodMapsATooManyRequestsToErrDisruptionBudget(t *testing.T) {
	pod := testNodePod("default", "web-1", "node-1", replicaSetOwnerRef("web"), false, false)
	client := fake.NewSimpleClientset(pod)

	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(ktesting.CreateAction)
		if !ok || create.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		return true, nil, apierrors.NewTooManyRequests("disruption budget would be violated", 5)
	})

	adapter := newTestAdapter("dev", client)
	err := adapter.EvictPod(context.Background(), "dev", "default", "web-1", -1)
	if !errors.Is(err, ports.ErrDisruptionBudget) {
		t.Errorf("EvictPod() error = %v, want %v", err, ports.ErrDisruptionBudget)
	}
}

func TestDrainCandidatesFillsMirrorAndLocalStorage(t *testing.T) {
	plain := testNodePod("default", "web-1", "node-1", replicaSetOwnerRef("web"), false, false)
	mirror := testNodePod("kube-system", "kube-apiserver-node-1", "node-1", nil, true, false)
	local := testNodePod("default", "cache-1", "node-1", replicaSetOwnerRef("cache"), false, true)

	client := fake.NewSimpleClientset(plain, mirror, local)
	adapter := newTestAdapter("dev", client)

	candidates, err := adapter.DrainCandidates(context.Background(), "dev", "node-1")
	if err != nil {
		t.Fatalf("DrainCandidates() error = %v", err)
	}

	byName := make(map[string]domain.DrainCandidate, len(candidates))
	for _, candidate := range candidates {
		byName[candidate.Pod.Name()] = candidate
	}

	if len(byName) != 3 {
		t.Fatalf("candidates = %d, want 3: %+v", len(byName), candidates)
	}
	if c := byName["web-1"]; c.Mirror || c.LocalStorage {
		t.Errorf("web-1 = %+v, want neither Mirror nor LocalStorage", c)
	}
	if c := byName["kube-apiserver-node-1"]; !c.Mirror {
		t.Errorf("kube-apiserver-node-1 = %+v, want Mirror", c)
	}
	if c := byName["cache-1"]; !c.LocalStorage {
		t.Errorf("cache-1 = %+v, want LocalStorage", c)
	}
}

// deletingEvictReactor simulates a real API server's eviction: on success it
// removes the pod from the tracker, so DrainNode's wait-for-gone poll (a Get)
// observes NotFound exactly as it would against a cluster. refuse reports
// whether THIS attempt for name should be turned away with a 429; it is
// checked and given the chance to flip on every call, which is what lets a
// test simulate "refused, then later allowed".
type deletingEvictReactor struct {
	t       *testing.T
	tracker ktesting.ObjectTracker
	refuse  func(name string, attempt int) bool

	mu       sync.Mutex
	attempts map[string]int
	actions  []string
}

func newDeletingEvictReactor(t *testing.T, tracker ktesting.ObjectTracker, refuse func(name string, attempt int) bool) *deletingEvictReactor {
	return &deletingEvictReactor{t: t, tracker: tracker, refuse: refuse, attempts: map[string]int{}}
}

func (r *deletingEvictReactor) react(action ktesting.Action) (bool, runtime.Object, error) {
	create, ok := action.(ktesting.CreateAction)
	if !ok || create.GetSubresource() != "eviction" {
		return false, nil, nil
	}
	eviction, ok := create.GetObject().(*policyv1.Eviction)
	if !ok {
		return false, nil, nil
	}

	r.mu.Lock()
	r.attempts[eviction.Name]++
	attempt := r.attempts[eviction.Name]
	r.actions = append(r.actions, eviction.Namespace+"/"+eviction.Name)
	r.mu.Unlock()

	if r.refuse != nil && r.refuse(eviction.Name, attempt) {
		return true, nil, apierrors.NewTooManyRequests("disruption budget would be violated", 0)
	}

	if err := r.tracker.Delete(podsGVR, eviction.Namespace, eviction.Name); err != nil {
		return true, nil, err
	}
	return true, nil, nil
}

func (r *deletingEvictReactor) evicted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.actions...)
}

func TestDrainNodeCordonsEvictsAndReportsSkips(t *testing.T) {
	node := testNode("node-1", false)
	deployPod := testNodePod("default", "web-1", "node-1", replicaSetOwnerRef("web"), false, false)
	daemonPod := testNodePod("default", "fluentd-1", "node-1", daemonSetOwnerRef("fluentd"), false, false)
	mirrorPod := testNodePod("kube-system", "kube-apiserver-node-1", "node-1", nil, true, false)

	client := fake.NewSimpleClientset(node, deployPod, daemonPod, mirrorPod)
	reactor := newDeletingEvictReactor(t, client.Tracker(), nil)
	client.PrependReactor("create", "pods", reactor.react)

	adapter := newTestAdapter("dev", client)
	report, err := adapter.DrainNode(context.Background(), "dev", "node-1", domain.DrainOptions{})
	if err != nil {
		t.Fatalf("DrainNode() error = %v", err)
	}

	if !report.Cordoned {
		t.Error("report.Cordoned = false, want true")
	}
	got, getErr := client.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("getting node: %v", getErr)
	}
	if !got.Spec.Unschedulable {
		t.Error("node was not actually cordoned")
	}

	if len(report.Evicted) != 1 || report.Evicted[0].Name() != "web-1" {
		t.Fatalf("Evicted = %+v, want exactly web-1", report.Evicted)
	}
	if len(report.Skipped) != 2 {
		t.Fatalf("Skipped = %+v, want 2 (daemonset + mirror)", report.Skipped)
	}
	if len(report.Refused) != 0 {
		t.Errorf("Refused = %+v, want none", report.Refused)
	}
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %+v, want none", report.Failed)
	}

	if evicted := reactor.evicted(); len(evicted) != 1 || evicted[0] != "default/web-1" {
		t.Errorf("eviction actions = %v, want exactly [default/web-1]", evicted)
	}

	// The two skipped pods must still exist: nothing was ever asked to
	// delete them.
	if _, err := client.CoreV1().Pods("default").Get(context.Background(), "fluentd-1", metav1.GetOptions{}); err != nil {
		t.Errorf("daemonset pod was removed, want it left alone: %v", err)
	}
	if _, err := client.CoreV1().Pods("kube-system").Get(context.Background(), "kube-apiserver-node-1", metav1.GetOptions{}); err != nil {
		t.Errorf("mirror pod was removed, want it left alone: %v", err)
	}
}

func TestDrainNodeRefusesABarePodWithoutForceAndEvictsNothing(t *testing.T) {
	node := testNode("node-1", false)
	bare := testNodePod("default", "standalone", "node-1", nil, false, false)

	client := fake.NewSimpleClientset(node, bare)
	reactor := newDeletingEvictReactor(t, client.Tracker(), nil)
	client.PrependReactor("create", "pods", reactor.react)

	adapter := newTestAdapter("dev", client)
	report, err := adapter.DrainNode(context.Background(), "dev", "node-1", domain.DrainOptions{})
	if !errors.Is(err, ports.ErrDrainRefused) {
		t.Fatalf("DrainNode() error = %v, want %v", err, ports.ErrDrainRefused)
	}

	if !report.Cordoned {
		t.Error("report.Cordoned = false, want true — cordoning happens before the plan is checked")
	}
	if len(report.Refused) != 1 {
		t.Fatalf("Refused = %+v, want exactly 1", report.Refused)
	}
	if len(report.Evicted) != 0 {
		t.Errorf("Evicted = %+v, want none", report.Evicted)
	}

	// The whole point of refusing the plan first: no eviction was ever
	// attempted for any pod.
	if evicted := reactor.evicted(); len(evicted) != 0 {
		t.Errorf("eviction actions = %v, want none", evicted)
	}

	if _, err := client.CoreV1().Pods("default").Get(context.Background(), "standalone", metav1.GetOptions{}); err != nil {
		t.Errorf("bare pod was removed, want it left alone: %v", err)
	}
}

func TestDrainNodeRetriesADisruptionBudgetRefusalUntilItClears(t *testing.T) {
	node := testNode("node-1", false)
	pod := testNodePod("default", "web-1", "node-1", replicaSetOwnerRef("web"), false, false)

	client := fake.NewSimpleClientset(node, pod)

	var refusals atomic.Int64
	reactor := newDeletingEvictReactor(t, client.Tracker(), func(_ string, attempt int) bool {
		// Refuse the first attempt only, so the second succeeds.
		if attempt == 1 {
			refusals.Add(1)
			return true
		}
		return false
	})
	client.PrependReactor("create", "pods", reactor.react)

	adapter := newTestAdapter("dev", client)

	// The retry backoff is real time (disruptionBudgetBackoff): the test
	// budget has to comfortably clear one wait, not be instant.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := adapter.DrainNode(ctx, "dev", "node-1", domain.DrainOptions{})
	if err != nil {
		t.Fatalf("DrainNode() error = %v", err)
	}

	if refusals.Load() != 1 {
		t.Fatalf("refusals = %d, want exactly 1 (then it cleared)", refusals.Load())
	}
	if len(report.Evicted) != 1 || report.Evicted[0].Name() != "web-1" {
		t.Fatalf("Evicted = %+v, want exactly web-1", report.Evicted)
	}
	if report.TimedOut {
		t.Error("report.TimedOut = true, want false: the retry succeeded well inside the timeout")
	}
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %+v, want none", report.Failed)
	}
}
