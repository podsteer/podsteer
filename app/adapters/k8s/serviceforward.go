package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
)

// ServiceForwardTarget resolves a Service into the pod and port a forward can
// actually be made to. See domain/serviceforward.go for the translation rules;
// this reads the two objects they need.
//
// TWO READS AND NO WATCH: the Service, and the pods its selector names. The
// pod list is a narrow one — same reasoning as findReplacementPod, which this
// deliberately mirrors — because a resolution that ensured a watch would leave
// a reflector behind for a Service somebody merely looked at.
func (a *Adapter) ServiceForwardTarget(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	service string,
	wantedPort string,
) (domain.ServiceForwardTarget, error) {
	op := fmt.Sprintf("resolving service %s/%s in %q", namespace, service, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ServiceForwardTarget{}, err
	}

	found, err := client.CoreV1().Services(namespace.String()).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return domain.ServiceForwardTarget{}, classify(op, err)
	}

	// An ExternalName is a DNS alias with nothing behind it in this cluster,
	// and a headless Service with hand-managed Endpoints has pods PodSteer
	// cannot find by label. Both are refused by the same sentence, because to
	// the operator they are the same fact: there is nothing here to forward to.
	if len(found.Spec.Selector) == 0 {
		return domain.ServiceForwardTarget{}, fmt.Errorf("%s: %w", op, domain.ErrServiceHasNoSelector)
	}

	chosen, err := domain.SelectServicePort(servicePorts(found), wantedPort)
	if err != nil {
		return domain.ServiceForwardTarget{}, fmt.Errorf("%s: %w", op, err)
	}

	pod, err := readyPodFor(ctx, client, namespace, found.Spec.Selector)
	if err != nil {
		return domain.ServiceForwardTarget{}, fmt.Errorf("%s: %w", op, err)
	}

	target, err := domain.ResolveTargetPort(chosen, containerPorts(pod))
	if err != nil {
		return domain.ServiceForwardTarget{}, fmt.Errorf("%s: %w", op, err)
	}

	return domain.ServiceForwardTarget{
		Pod:           pod.Name,
		PodUID:        string(pod.UID),
		ContainerPort: target,
		ServicePort:   chosen.Port,
		PortName:      chosen.Name,
		Protocol:      chosen.Protocol,
		Selector:      found.Spec.Selector,
	}, nil
}

// servicePorts maps the Service's ports into the shape the domain reasons
// about. `intstr` is flattened to its string form deliberately: the domain
// then has one case to handle rather than two representations of the same
// question, and "is this a number" is a parse it already has to do for the
// operator's own input.
func servicePorts(service *corev1.Service) []domain.ServicePort {
	ports := make([]domain.ServicePort, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		target := port.TargetPort.String()
		// intstr renders an unset value as "0", which is not the same as
		// unset: Kubernetes defaults an absent targetPort to the service
		// port, and a literal 0 is not a port anything listens on.
		if target == "0" {
			target = ""
		}
		ports = append(ports, domain.ServicePort{
			Name:       port.Name,
			Port:       int(port.Port),
			TargetPort: target,
			Protocol:   string(port.Protocol),
		})
	}
	return ports
}

// readyPodFor finds a pod the Service would send traffic to.
//
// READY AND ON A NODE, which is the same test findReplacementPod applies: a
// pod that is scheduled but not ready is one the Service itself is not sending
// traffic to, and forwarding to it would produce a connection that refuses
// while the Service works perfectly.
// A FREE FUNCTION rather than a method: it needs a client and nothing else
// the adapter holds, and a test can then hand it a fake clientset directly.
func readyPodFor(
	ctx context.Context,
	client kubernetes.Interface,
	namespace domain.NamespaceName,
	selector map[string]string,
) (*corev1.Pod, error) {
	list, err := client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set(selector).String(),
	})
	if err != nil {
		return nil, classify("listing the pods behind the service", err)
	}

	running := 0
	for index := range list.Items {
		pod := &list.Items[index]
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		running++
		if podIsReady(pod) {
			return pod, nil
		}
	}

	// A running-but-unready pod is reported as no endpoint rather than
	// forwarded to: the Service is not sending it traffic either, and a
	// forward that connects to a pod failing its readiness probe looks like
	// the application being broken rather than the pod being excluded.
	//
	// The two cases share a sentinel and NOT a sentence, because they send the
	// operator to different places: pods that are up and failing a readiness
	// probe is an application problem, and no pod at all behind the selector
	// is a Service that selects nothing — a typo in the selector, or a
	// workload that never rolled out.
	if running > 0 {
		return nil, fmt.Errorf("%w: %d running, none of them ready", domain.ErrNoReadyEndpoint, running)
	}
	return nil, fmt.Errorf("%w: no running pod matches its selector", domain.ErrNoReadyEndpoint)
}

// podIsReady reads the condition the endpoints controller reads.
func podIsReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// containerPorts collects every container's declared ports, for resolving a
// named targetPort. Init containers are included: a sidecar declared as a
// restartable init container is a running listener in every version that
// supports one, and leaving it out would refuse a name that resolves.
func containerPorts(pod *corev1.Pod) []domain.ContainerPort {
	var ports []domain.ContainerPort
	for _, container := range append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		for _, port := range container.Ports {
			if port.Name == "" {
				continue
			}
			ports = append(ports, domain.ContainerPort{Name: port.Name, Port: int(port.ContainerPort)})
		}
	}
	return ports
}

// compile-time proof the adapter still satisfies the port it grew a method on.
var _ interface {
	ServiceForwardTarget(context.Context, domain.ClusterID, domain.NamespaceName, string, string) (domain.ServiceForwardTarget, error)
} = (*Adapter)(nil)
