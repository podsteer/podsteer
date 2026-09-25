package k8s

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// terminatingPod marks a pod for deletion while leaving it Ready, which is
// what a pod inside its grace period actually looks like.
func terminatingPod(name string) *corev1.Pod {
	pod := richPod(name)
	deleted := metav1.NewTime(time.Now().Add(-5 * time.Second))
	pod.DeletionTimestamp = &deleted
	return pod
}

// revisionPod is a pod of one specific ReplicaSet revision.
func revisionPod(name, hash string) *corev1.Pod {
	pod := richPod(name)
	pod.Labels["pod-template-hash"] = hash
	return pod
}

// TestFindReplacementPodPrefersTheOriginalPod is the reconnect case the
// supervisor could not serve.
//
// A forward drops for two reasons and only one of them is a dead pod: the
// SPDY connection also goes when a load balancer's idle timeout fires, when
// the API server rolls, and when a VPN re-keys. The search used to skip
// forward.Pod outright, so a single-replica workload had no candidate at all
// — two minutes of "Reconnecting" and then the row disappeared, with the pod
// still running and still Ready on the other side.
func TestFindReplacementPodPrefersTheOriginalPod(t *testing.T) {
	pollingLists(t)

	tests := []struct {
		name     string
		pods     []*corev1.Pod
		pod      string
		selector map[string]string
		want     string
	}{
		{
			name:     "the connection dropped and the pod is still there",
			pods:     []*corev1.Pod{richPod("api-0")},
			pod:      "api-0",
			selector: map[string]string{"app": "web"},
			want:     "api-0",
		},
		{
			name:     "the original wins over a healthy sibling",
			pods:     []*corev1.Pod{richPod("api-1"), richPod("api-0")},
			pod:      "api-0",
			selector: map[string]string{"app": "web"},
			// Moving to a sibling would silently change which replica the
			// operator's client is talking to — different logs, different
			// in-memory state, a different member of a StatefulSet.
			want: "api-0",
		},
		{
			name:     "a bare pod with no selector is still reconnectable",
			pods:     []*corev1.Pod{richPod("api-0")},
			pod:      "api-0",
			selector: nil,
			// This returned immediately with nothing before: no selector was
			// read as nothing to reconnect to, when the pod itself was.
			want: "api-0",
		},
		{
			name:     "but a bare pod's neighbour is not a replacement",
			pods:     []*corev1.Pod{richPod("api-1")},
			pod:      "api-0",
			selector: nil,
			want:     "",
		},
		{
			name:     "the pod died, so a sibling of the same revision serves",
			pods:     []*corev1.Pod{richPod("api-1")},
			pod:      "api-0",
			selector: map[string]string{"app": "web"},
			want:     "api-1",
		},
		{
			name:     "an original inside its grace period is not offered",
			pods:     []*corev1.Pod{terminatingPod("api-0"), richPod("api-1")},
			pod:      "api-0",
			selector: map[string]string{"app": "web"},
			// A pod keeps Ready through termination, so taking it would
			// establish a forward that dies again seconds later.
			want: "api-1",
		},
		{
			name:     "nor is a terminating sibling",
			pods:     []*corev1.Pod{terminatingPod("api-1")},
			pod:      "api-0",
			selector: map[string]string{"app": "web"},
			want:     "",
		},
		{
			name:     "a pod of the revision that rolled out since is not offered",
			pods:     []*corev1.Pod{revisionPod("api-1", "7d9f")},
			pod:      "api-0",
			selector: map[string]string{"app": "web", "pod-template-hash": "5c4b"},
			want:     "",
		},
		{
			name:     "and neither is the original name at a new revision",
			pods:     []*corev1.Pod{revisionPod("db-0", "7d9f")},
			pod:      "db-0",
			selector: map[string]string{"app": "web", "pod-template-hash": "5c4b"},
			// A StatefulSet member keeps its name across a re-creation, so a
			// name match alone would put the forward on new code.
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := watchedClient(t, test.pods...)
			adapter := newForwardTestAdapter(t, "dev", client)

			entry := &forwarder{
				forward: domain.Forward{
					ID:        "1",
					ClusterID: "dev",
					Namespace: domain.NamespaceName("web"),
					Pod:       test.pod,
					Selector:  test.selector,
				},
				stop: make(chan struct{}),
				done: make(chan struct{}),
			}

			got, err := adapter.findReplacementPod(entry, entry.snapshot())
			if err != nil {
				t.Fatalf("findReplacementPod() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("findReplacementPod() = %q, want %q", got, test.want)
			}
		})
	}
}
