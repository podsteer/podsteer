package k8s

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/podsteer/podsteer/app/domain"
)

// synthPods returns n pods shaped like a real namespace's: the labels a
// Deployment stamps, an owner, two containers with requests and limits, and
// a status with both containers reported — the fields mapPod actually walks.
// A pod with nothing on it would benchmark the allocator, not the mapper.
func synthPods(n int) []*corev1.Pod {
	created := metav1.NewTime(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC))
	controller := true

	pods := make([]*corev1.Pod, 0, n)
	for i := range n {
		app := fmt.Sprintf("service-%03d", i%250)
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("%s-7d9f8c6b5-%05d", app, i),
				Namespace:         fmt.Sprintf("team-%02d", i%20),
				UID:               types.UID(fmt.Sprintf("7c9e6679-7425-40de-944b-%012d", i)),
				CreationTimestamp: created,
				Labels: map[string]string{
					"app.kubernetes.io/name":     app,
					"app.kubernetes.io/instance": app + "-prod",
					"pod-template-hash":          "7d9f8c6b5",
				},
				Annotations: map[string]string{
					"kubectl.kubernetes.io/restartedAt": "2026-09-01T08:00:00Z",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "ReplicaSet", Name: app + "-7d9f8c6b5", Controller: &controller},
				},
			},
			Spec: corev1.PodSpec{
				NodeName: fmt.Sprintf("worker-%03d", i%100),
				Containers: []corev1.Container{
					benchContainer("app", "registry.example.com/"+app+":1.4.2", "250m", "256Mi"),
					benchContainer("istio-proxy", "docker.io/istio/proxyv2:1.23.0", "100m", "128Mi"),
				},
			},
			Status: corev1.PodStatus{
				Phase:    corev1.PodRunning,
				PodIP:    fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256),
				QOSClass: corev1.PodQOSBurstable,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				},
				ContainerStatuses: []corev1.ContainerStatus{
					benchStatus("app", int32(i%4)),
					benchStatus("istio-proxy", 0),
				},
			},
		})
	}
	return pods
}

func benchContainer(name, image, cpu, memory string) corev1.Container {
	return corev1.Container{
		Name:  name,
		Image: image,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
	}
}

func benchStatus(name string, restarts int32) corev1.ContainerStatus {
	started := true
	return corev1.ContainerStatus{
		Name:         name,
		Ready:        true,
		Started:      &started,
		RestartCount: restarts,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Date(2026, 9, 1, 8, 1, 0, 0, time.UTC))},
		},
	}
}

func BenchmarkMapPods10k(b *testing.B) {
	pods := synthPods(10_000)
	cluster := domain.ClusterID("kind-scale")
	projection := domain.Projection{}

	b.ReportAllocs()
	for b.Loop() {
		for _, pod := range pods {
			if _, err := mapPod(cluster, pod, projection); err != nil {
				b.Fatalf("mapPod() error = %v", err)
			}
		}
	}
}
