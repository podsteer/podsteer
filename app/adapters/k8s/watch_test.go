package k8s

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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

	"github.com/podsteer/podsteer/app/domain"
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
				TTY:     true,
				Stdin:   true,
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

	// Mapped UNDER A PROJECTION, because annotations are now something
	// mapPod reads — and the projection deliberately names the one
	// annotation the store strips beside an ordinary one. The domain
	// refuses the former (see domain.NewProjection), which is exactly what
	// keeps the two copies equal below. Remove that refusal and this fails,
	// as it should: a column of the last-applied manifest would read blank
	// on a cluster the watch is serving and the whole manifest on one it is
	// not.
	projection := contractProjection()

	want, err := mapPod("dev", original, projection)
	if err != nil {
		t.Fatalf("mapPod(original) error = %v", err)
	}
	got, err := mapPod("dev", stripped.(*corev1.Pod), projection)
	if err != nil {
		t.Fatalf("mapPod(stripped) error = %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("stripping changed what the application sees:\n original: %+v\n stripped: %+v", want, got)
	}
	if got.Annotations()["keep"] != "this" {
		t.Fatalf("the projected annotation did not survive the store: %v", got.Annotations())
	}
}

// contractProjection is what every stripping contract test maps under: an
// ordinary annotation the fixtures carry, plus the one the store removes.
func contractProjection() domain.Projection {
	return domain.NewProjection([]string{"keep", corev1.LastAppliedConfigAnnotation})
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
	manager := newWatchManager(true, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, recheckEvery)
	return manager, func() (kubernetes.Interface, error) { return client, nil }
}

func TestTheStoreServesOnlyOnceItHasSynced(t *testing.T) {
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager, supply := newTestManager(client)
	defer manager.stopAll()

	// Before anything is started, and while it is starting, a read is told to
	// go to the cluster — never to wait.
	if _, serving := watched[*corev1.Pod](manager, "dev", watchPods); serving {
		t.Fatal("served from a store nobody had started")
	}

	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})

	pods, _ := watched[*corev1.Pod](manager, "dev", watchPods)
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
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
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
		return running && set.kinds[watchPods].get() == watchDegraded
	})

	if _, serving := watched[*corev1.Pod](manager, "dev", watchPods); serving {
		t.Fatal("a store whose watch was refused is still answering reads")
	}
}

func TestForgettingAClusterStopsItsGoroutinesAndWaits(t *testing.T) {
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager, supply := newTestManager(client)

	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})

	manager.forget("dev")

	if _, serving := watched[*corev1.Pod](manager, "dev", watchPods); serving {
		t.Fatal("a forgotten cluster is still answering reads")
	}
	// A set is never reused: reconnecting builds a new one, so a reflector
	// from before the disconnect cannot write into a live store.
	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})
	manager.stopAll()
}

func TestNothingIsWatchedWhenTheFeatureIsOff(t *testing.T) {
	// Off is not an approximation of the old behaviour — it is the same code
	// path, because the fallback IS that path.
	client, lists := watchedClient(t, richPod("api-1"))
	manager := newWatchManager(false, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, recheckEvery)
	defer manager.stopAll()

	manager.ensure("dev", func() (kubernetes.Interface, error) { return client, nil })
	time.Sleep(20 * time.Millisecond)

	if _, serving := watched[*corev1.Pod](manager, "dev", watchPods); serving {
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

	all, err := mapWatchedPods("dev", watched, "", domain.Projection{})
	if err != nil {
		t.Fatalf("mapWatchedPods() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("mapped %d pods, want the 2 that map", len(all))
	}

	web, err := mapWatchedPods("dev", watched, "web", domain.Projection{})
	if err != nil {
		t.Fatalf("mapWatchedPods() error = %v", err)
	}
	if len(web) != 1 || web[0].Namespace() != "web" {
		t.Fatalf("narrowing to web gave %d pods", len(web))
	}
}

func richReplicaSet(name string) *appsv1.ReplicaSet {
	template := richPod("template")
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "web",
			Labels:    map[string]string{"app": "web"},
			Annotations: map[string]string{
				corev1.LastAppliedConfigAnnotation: `{"a":"very long manifest"}`,
				"keep":                             "this",
			},
			ManagedFields:     []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: boolPtr(true)}},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: func() *int32 { r := int32(3); return &r }(),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       template.Spec,
			},
		},
		Status: appsv1.ReplicaSetStatus{Replicas: 3, ReadyReplicas: 2, AvailableReplicas: 2},
	}
}

func richJob(name string) *batchv1.Job {
	template := richPod("template")
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:          name,
			Namespace:     "web",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			Annotations: map[string]string{
				corev1.LastAppliedConfigAnnotation: `{"a":"long"}`,
			},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "CronJob", Name: "nightly", Controller: boolPtr(true)}},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
		Spec: batchv1.JobSpec{
			Completions: func() *int32 { c := int32(1); return &c }(),
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"job": name}},
			Template:    corev1.PodTemplateSpec{Spec: template.Spec},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}
}

func TestStrippingAControllerChangesNothingThisApplicationReads(t *testing.T) {
	// One contract test per transform, for the same reason the pod one
	// exists: a field the mapper reads and the transform removes goes quietly
	// blank on exactly the clusters where the watch is serving, and stays
	// correct everywhere else.
	t.Run("replicaset", func(t *testing.T) {
		original := richReplicaSet("web-abc")

		stripped, err := stripReplicaSet(original.DeepCopy())
		if err != nil {
			t.Fatalf("stripReplicaSet() error = %v", err)
		}

		want, err := mapReplicaSet("dev", original, contractProjection())
		if err != nil {
			t.Fatalf("mapReplicaSet(original) error = %v", err)
		}
		got, err := mapReplicaSet("dev", stripped.(*appsv1.ReplicaSet), contractProjection())
		if err != nil {
			t.Fatalf("mapReplicaSet(stripped) error = %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("stripping changed what the application sees:\n want: %+v\n got:  %+v", want, got)
		}
		if got.Annotations()["keep"] != "this" {
			t.Fatalf("the projected annotation did not survive the store: %v", got.Annotations())
		}
	})

	t.Run("job", func(t *testing.T) {
		original := richJob("nightly-1")

		stripped, err := stripJob(original.DeepCopy())
		if err != nil {
			t.Fatalf("stripJob() error = %v", err)
		}

		want, err := mapJob("dev", original, contractProjection())
		if err != nil {
			t.Fatalf("mapJob(original) error = %v", err)
		}
		got, err := mapJob("dev", stripped.(*batchv1.Job), contractProjection())
		if err != nil {
			t.Fatalf("mapJob(stripped) error = %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("stripping changed what the application sees:\n want: %+v\n got:  %+v", want, got)
		}
	})
}

func TestAControllersPodTemplateIsReducedToItsImages(t *testing.T) {
	// The reason these two are watched at all. A ReplicaSet carries a whole
	// pod template and the mapper reads one field out of it, so a namespace
	// with ten revisions holds ten copies of a spec nothing displays.
	stripped, err := stripReplicaSet(richReplicaSet("web-abc"))
	if err != nil {
		t.Fatalf("stripReplicaSet() error = %v", err)
	}

	set := stripped.(*appsv1.ReplicaSet)
	container := set.Spec.Template.Spec.Containers[0]
	switch {
	case container.Image == "":
		t.Fatal("the image was stripped — it is the one field the mapper reads")
	case container.Env != nil || container.VolumeMounts != nil:
		t.Fatal("the template's display material survived")
	case set.Spec.Selector == nil:
		t.Fatal("the selector was stripped — attribution reads it")
	case set.Status.ReadyReplicas != 2:
		t.Fatal("the replica counts were stripped — they are the columns")
	}
}

func TestAWatchNobodyIsReadingIsStopped(t *testing.T) {
	// Somebody who set refresh to "manual only" chose to stop talking to
	// their cluster, and a stream left open between button presses is not
	// that. The next read starts a fresh set exactly as the first one did.
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager := newWatchManager(true, slog.New(slog.DiscardHandler), 50*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer manager.stopAll()

	supply := func() (kubernetes.Interface, error) { return client, nil }
	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})

	// Stop reading it.
	waitFor(t, func() bool {
		manager.mu.Lock()
		_, running := manager.sets["dev"]
		manager.mu.Unlock()
		return !running
	})

	// And it comes back on the next read.
	manager.ensure("dev", supply)
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})
}

func TestReadingKeepsAWatchAlive(t *testing.T) {
	// The other half: an ordinary session refreshes well inside the idle
	// window, and reaping a set somebody is watching would turn every refresh
	// into a fresh initial list.
	pollingLists(t)

	client, _ := watchedClient(t, richPod("api-1"))
	manager := newWatchManager(true, slog.New(slog.DiscardHandler), 200*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer manager.stopAll()

	manager.ensure("dev", func() (kubernetes.Interface, error) { return client, nil })
	waitFor(t, func() bool {
		_, serving := watched[*corev1.Pod](manager, "dev", watchPods)
		return serving
	})

	// Read across more than one idle window.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, serving := watched[*corev1.Pod](manager, "dev", watchPods); !serving {
			t.Fatal("a set being read every few milliseconds was reaped")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestATransientErrorDoesNotCondemnAStoreForever(t *testing.T) {
	// THE BUG THIS GUARDS WAS SILENT AND PERMANENT. A transient watch error —
	// an API server restart, a reset connection — demoted the store so reads
	// went back to the network, and nothing ever promoted it again. The
	// reflector carried on mirroring the cluster perfectly for the life of
	// the connection while every read paid the full list this exists to
	// avoid, and because a read still counts as activity the set was never
	// reaped either. Nothing failed; it just quietly stopped helping.
	manager := newWatchManager(true, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, time.Millisecond)
	defer manager.stopAll()

	store := &kindWatch{}
	store.set(watchServing)

	// The error arrives: the reflector had reached "100" when it stopped
	// delivering.
	stalled := "100"
	store.state.Store(int32(watchStarting))
	store.stalled.Store(&stalled)

	// It relists and moves past it, which is the only evidence available
	// that the stream is live again.
	moved := make(chan struct{})
	version := func() string {
		select {
		case <-moved:
			return "137"
		default:
			return "100"
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.supervise(ctx, "dev", store, watchPods, version)
	}()

	// Still stalled, so still not answering.
	time.Sleep(20 * time.Millisecond)
	if store.get() != watchStarting {
		t.Fatalf("resumed with no evidence the reflector recovered: %v", store.get())
	}

	close(moved)
	waitFor(t, func() bool { return store.get() == watchServing })

	cancel()
	<-done
}

func TestASupervisorLeavesACondemnedStoreCondemned(t *testing.T) {
	// A refusal is a decision, not a blip. An account that may list and not
	// watch must never be promoted by anything, however far the version moves.
	manager := newWatchManager(true, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, time.Millisecond)
	defer manager.stopAll()

	store := &kindWatch{}
	store.set(watchDegraded)
	stalled := "100"
	store.stalled.Store(&stalled)

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.supervise(t.Context(), "dev", store, watchPods, func() string { return "999" })
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the supervisor kept watching a store nothing will ever update")
	}
	if store.get() != watchDegraded {
		t.Fatalf("a condemned store was promoted: %v", store.get())
	}
}
