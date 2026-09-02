package k8s

import (
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clientfeatures "k8s.io/client-go/features"
	featuretesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// richPod carries every field a real one does that this package might read,
// so the transform is tested against something worth stripping.
func richPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "web",
			UID:       types.UID("uid-" + name),
			Labels:    map[string]string{"app": "web"},
			Annotations: map[string]string{
				corev1.LastAppliedConfigAnnotation: `{"a":"very long manifest"}`,
				"keep":                             "this",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: name + "-rs", Controller: boolPtr(true)},
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
			Finalizers:        []string{"kubernetes"},
		},
		Spec: corev1.PodSpec{
			NodeName:     "node-1",
			NodeSelector: map[string]string{"disk": "ssd"},
			Tolerations:  []corev1.Toleration{{Key: "spot"}},
			Volumes:      []corev1.Volume{{Name: "config"}},
			Containers: []corev1.Container{{
				Name:    "app",
				Image:   "nginx:1.27",
				Command: []string{"/bin/sh"},
				Args:    []string{"-c", "sleep"},
				Env:     []corev1.EnvVar{{Name: "SECRET", Value: "x"}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
				},
				VolumeMounts:  []corev1.VolumeMount{{Name: "config", MountPath: "/etc"}},
				LivenessProbe: &corev1.Probe{InitialDelaySeconds: 10},
			}},
		},
		Status: corev1.PodStatus{
			Phase:    corev1.PodRunning,
			PodIP:    "10.0.0.1",
			QOSClass: corev1.PodQOSBurstable,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true, Image: "nginx:1.27", RestartCount: 2,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func boolPtr(value bool) *bool { return &value }

func TestStrippingAPodChangesNothingThisApplicationReads(t *testing.T) {
	// THE TRAP IN THE WHOLE APPROACH, and the reason this test is not
	// optional. The store holds stripped pods, so anything mapPod reads that
	// stripPod removes goes quietly blank on clusters where the watch happens
	// to be serving — and stays correct everywhere else, which is the worst
	// possible way to find out.
	//
	// The day somebody extends mapPod to read tolerations or env, this fails.
	original := richPod("api-1")

	stripped, err := stripPod(original.DeepCopy())
	if err != nil {
		t.Fatalf("stripPod() error = %v", err)
	}

	want, err := mapPod("dev", original)
	if err != nil {
		t.Fatalf("mapPod(original) error = %v", err)
	}
	got, err := mapPod("dev", stripped.(*corev1.Pod))
	if err != nil {
		t.Fatalf("mapPod(stripped) error = %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("stripping changed what the application sees:\n original: %+v\n stripped: %+v", want, got)
	}
}

func TestStrippingActuallyRemovesTheBulk(t *testing.T) {
	// A transform that keeps everything is a transform that does nothing.
	stripped, err := stripPod(richPod("api-1"))
	if err != nil {
		t.Fatalf("stripPod() error = %v", err)
	}

	pod := stripped.(*corev1.Pod)
	switch {
	case pod.ManagedFields != nil:
		t.Fatal("managed fields survived, which are usually the largest part of a pod")
	case pod.Annotations[corev1.LastAppliedConfigAnnotation] != "":
		t.Fatal("the last-applied manifest survived")
	case pod.Annotations["keep"] != "this":
		t.Fatal("an ordinary annotation was removed")
	case pod.Spec.Containers[0].Env != nil:
		t.Fatal("container environment survived")
	case pod.Spec.Containers[0].Resources.Requests == nil:
		t.Fatal("container requests were stripped — every meter is computed from them")
	}
}

// watchedClient builds a fake clientset serving one pod, with hooks to count
// calls and to fail the watch.
func watchedClient(t *testing.T, pods ...*corev1.Pod) (*fake.Clientset, *atomic.Int64) {
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
	return client, &lists
}

// pollingLists makes the reflector do an ordinary LIST then WATCH.
//
// client-go now streams the initial list through the watch by default
// (WatchListClient), and the fake clientset does not implement the initial
// events that protocol needs — so a store built over a fake never syncs. That
// is a limitation of the double, not of the design: a real API server that
// does not support streaming lists makes client-go fall back to exactly the
// path this pins.
func pollingLists(t *testing.T) {
	t.Helper()
	featuretesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
}

func newTestManager(client kubernetes.Interface) (*watchManager, func() (kubernetes.Interface, error)) {
	manager := newWatchManager(true, slog.New(slog.DiscardHandler))
	return manager, func() (kubernetes.Interface, error) { return client, nil }
}

func TestTheStoreServesOnlyOnceItHasSynced(t *testing.T) {
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager, supply := newTestManager(client)
	defer manager.stopAll()

	// Before anything is started, and while it is starting, a read is told to
	// go to the cluster — never to wait.
	if _, serving := manager.pods("dev"); serving {
		t.Fatal("served from a store nobody had started")
	}

	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := manager.pods("dev")
		return serving
	})

	pods, _ := manager.pods("dev")
	if len(pods) != 1 {
		t.Fatalf("store holds %d pods, want 1", len(pods))
	}
}

func TestOnlyOneWatchIsStartedHoweverManyReadsArrive(t *testing.T) {
	pollingLists(t)

	// Every refresh fires several reads at once, and a cold cluster gets them
	// all before any watch exists.
	client, lists := watchedClient(t, richPod("api-1"))
	manager, supply := newTestManager(client)
	defer manager.stopAll()

	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			manager.ensure("dev", supply)
		}()
	}
	group.Wait()

	waitFor(t, func() bool {
		_, serving := manager.pods("dev")
		return serving
	})
	// One reflector, so one initial list.
	if got := lists.Load(); got != 1 {
		t.Fatalf("started %d reflectors for 16 concurrent reads, want 1", got)
	}
}

func TestAnAccountThatMayNotWatchIsNotWatched(t *testing.T) {
	pollingLists(t)

	// The ordinary list/get Role: the initial list succeeds and the watch is
	// refused. A store that synced and was then condemned must never answer —
	// it would be a snapshot frozen at connection time.
	client, _ := watchedClient(t, richPod("api-1"))
	client.PrependWatchReactor("pods", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "pods"}, "", nil)
	})

	manager, supply := newTestManager(client)
	defer manager.stopAll()

	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		manager.mu.Lock()
		set, running := manager.sets["dev"]
		manager.mu.Unlock()
		return running && set.pods.get() == watchDegraded
	})

	if _, serving := manager.pods("dev"); serving {
		t.Fatal("a store whose watch was refused is still answering reads")
	}
}

func TestForgettingAClusterStopsItsGoroutinesAndWaits(t *testing.T) {
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager, supply := newTestManager(client)

	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := manager.pods("dev")
		return serving
	})

	manager.forget("dev")

	if _, serving := manager.pods("dev"); serving {
		t.Fatal("a forgotten cluster is still answering reads")
	}
	// A set is never reused: reconnecting builds a new one, so a reflector
	// from before the disconnect cannot write into a live store.
	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := manager.pods("dev")
		return serving
	})
	manager.stopAll()
}

func TestNothingIsWatchedWhenTheFeatureIsOff(t *testing.T) {
	// Off is not an approximation of the old behaviour — it is the same code
	// path, because the fallback IS that path.
	client, lists := watchedClient(t, richPod("api-1"))
	manager := newWatchManager(false, slog.New(slog.DiscardHandler))
	defer manager.stopAll()

	manager.ensure("dev", func() (kubernetes.Interface, error) { return client, nil })
	time.Sleep(20 * time.Millisecond)

	if _, serving := manager.pods("dev"); serving {
		t.Fatal("served from a store with the feature switched off")
	}
	if got := lists.Load(); got != 0 {
		t.Fatalf("made %d requests with the feature switched off, want none", got)
	}
}

func TestNarrowingTheStoreToOneNamespace(t *testing.T) {
	// The store is cluster-wide, and most reads are not. An unmappable object
	// is skipped rather than failing the read: one bad pod must not empty a
	// list of five thousand, which is how the list path already behaves.
	watched := []*corev1.Pod{
		richPod("api-1"),
		func() *corev1.Pod {
			other := richPod("dns-1")
			other.Namespace = "kube-system"
			return other
		}(),
		func() *corev1.Pod {
			broken := richPod("broken")
			broken.Namespace = "NOT A NAMESPACE"
			return broken
		}(),
	}

	all, err := mapWatchedPods("dev", watched, "")
	if err != nil {
		t.Fatalf("mapWatchedPods() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("mapped %d pods, want the 2 that map", len(all))
	}

	web, err := mapWatchedPods("dev", watched, "web")
	if err != nil {
		t.Fatalf("mapWatchedPods() error = %v", err)
	}
	if len(web) != 1 || web[0].Namespace() != "web" {
		t.Fatalf("narrowing to web gave %d pods", len(web))
	}
}
