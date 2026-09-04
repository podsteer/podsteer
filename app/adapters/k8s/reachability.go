package k8s

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Two vantages, two entirely different mechanisms, and the difference between
// them is the feature. See domain/reachability.go for the rules; this file is
// only the sockets.
//
// Neither vantage contacts anything but the cluster. The local one reaches it
// through the API server named in the kubeconfig — its service proxy, or a
// port-forward tunnelled through it — and the in-cluster one reaches nothing
// at all from this machine: it asks a container to try, over the same exec
// session a file copy uses.

// probeOutputCap bounds what is kept of a probe's stdout. The script writes
// three short lines; a container that writes megabytes to stdout in answer is
// not one whose output is worth holding.
const probeOutputCap = 8 * 1024

// ProbeFromHere performs plan from this machine. See ports.InspectPort.
func (a *Adapter) ProbeFromHere(ctx context.Context, id domain.ClusterID, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	// The plan's own timeout, applied here and not merely passed along: a
	// probe is somebody waiting, and every path below has to be bounded by
	// the same number the panel already told them about.
	ctx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	switch plan.Route {
	case domain.RouteServiceProxy:
		return a.probeServiceProxy(ctx, id, plan)
	case domain.RoutePortForward:
		return a.probePortForward(ctx, id, plan)
	default:
		return domain.ProbeObservation{}, fmt.Errorf("probing %s: %w", plan.Address(), domain.ErrProbeVantageUnavailable)
	}
}

// probeServiceProxy asks the API server to fetch "/" from a Service.
//
// ONE REQUEST, and what it is evidence of is stated rather than implied: the
// API server made the connection, so a success says the Service's endpoints
// answer it — not that a workload elsewhere in the cluster may reach them.
func (a *Adapter) probeServiceProxy(ctx context.Context, id domain.ClusterID, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ProbeObservation{}, err
	}

	op := fmt.Sprintf("probing service %s/%s:%d in %q", plan.Namespace, plan.Name, plan.Port, id)

	started := time.Now()
	_, err = client.CoreV1().
		Services(plan.Namespace.String()).
		ProxyGet(plan.Scheme, plan.Name, strconv.Itoa(plan.Port), "/", nil).
		DoRaw(ctx)

	return classifyProxyOutcome(op, err, time.Since(started))
}

// classifyProxyOutcome turns what the service proxy returned into an
// observation, or into an error when the probe could not be performed at all.
//
// A pure function of the error, so the three cases that matter can be argued
// with in a test rather than reproduced against a cluster. They are:
//
//   - The account may not use the proxy subresource. Not an answer about the
//     Service at all — reported as an error so the pane says so rather than
//     drawing a red cross the operator would read as "my Service is down".
//   - The API server reached the endpoints and they answered something. Any
//     status is an answer: a 404 or a 500 means a process accepted the
//     connection and replied, which is precisely what was being asked.
//   - The API server could not reach the endpoints. Kubernetes reports that
//     as 503 with the dial error in its message, and that message is carried
//     verbatim because it names the address and the reason.
func classifyProxyOutcome(op string, err error, elapsed time.Duration) (domain.ProbeObservation, error) {
	observation := domain.ProbeObservation{
		Elapsed: elapsed,
		Steps: []domain.ProbeStep{{
			Name:   domain.StepDNS,
			Status: domain.StatusSkipped,
			Detail: "the API server resolved and dialled the endpoint on your behalf",
		}},
	}

	if err == nil {
		observation.Steps = append(observation.Steps,
			domain.ProbeStep{Name: domain.StepConnect, Status: domain.StatusOK, Detail: "the API server reached the service"},
			domain.ProbeStep{Name: domain.StepHTTP, Status: domain.StatusOK, Detail: "the service answered"},
		)
		observation.StatusCode = http.StatusOK
		return observation, nil
	}

	// An RBAC refusal of services/proxy names the subresource. A 403 the
	// SERVICE itself returned does not, and is an answer rather than a
	// refusal — telling those apart on the message is the only signal
	// Kubernetes offers, since both arrive as a StatusError carrying 403.
	if refusedSubresource(err) {
		return domain.ProbeObservation{}, classify(op, err)
	}

	var status apierrors.APIStatus
	if errors.As(err, &status) {
		code := int(status.Status().Code)
		message := strings.TrimSpace(status.Status().Message)

		if code == http.StatusServiceUnavailable {
			observation.Steps = append(observation.Steps, domain.ProbeStep{
				Name:   domain.StepConnect,
				Status: domain.StatusFailed,
				Detail: firstNonEmpty(message, "the API server could not reach the service"),
			})
			return observation, nil
		}

		if code >= 100 && code <= 599 {
			observation.StatusCode = code
			observation.Steps = append(observation.Steps,
				domain.ProbeStep{Name: domain.StepConnect, Status: domain.StatusOK, Detail: "the API server reached the service"},
				domain.ProbeStep{Name: domain.StepHTTP, Status: domain.StatusOK, Detail: message},
			)
			return observation, nil
		}
	}

	// Anything left is a failure of the request itself rather than an answer
	// about the Service: the cluster is unreachable, the credentials expired,
	// the probe was cancelled.
	return domain.ProbeObservation{}, classify(op, err)
}

// refusedSubresource reports whether err is RBAC declining services/proxy,
// as opposed to the proxied service answering 401 or 403 itself.
func refusedSubresource(err error) bool {
	if !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "services/proxy") ||
		strings.Contains(message, "cannot get resource") ||
		strings.Contains(message, "is forbidden: user")
}

// probePortForward opens an ephemeral forward, dials it, and tears it down.
//
// REUSES THE FORWARD MACHINERY RATHER THAN OPENING A SECOND SPDY DIALER, and
// that is worth stating because a second implementation was the obvious thing
// to write: it would have had its own timeout, its own teardown and its own
// way of leaking a bound socket, and the whole reason forwards live in one
// registry is that the record and the goroutine must never part company.
//
// The forward is stopped in a defer, so it goes whatever happens next —
// including a cancelled context, which is the case that leaks in every client
// that gets this wrong. StopPortForward waits, so the local port is genuinely
// released before this returns.
func (a *Adapter) probePortForward(ctx context.Context, id domain.ClusterID, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	forward, err := a.StartPortForward(ctx, id, plan.Namespace, plan.Name, "", 0, plan.Port, "", "TCP", nil)
	if err != nil {
		return domain.ProbeObservation{}, err
	}
	defer func() { _ = a.StopPortForward(forward.ID) }()

	local := net.JoinHostPort("127.0.0.1", strconv.Itoa(forward.LocalPort))
	observation := domain.ProbeObservation{
		Steps: []domain.ProbeStep{{
			Name:   domain.StepDNS,
			Status: domain.StatusSkipped,
			Detail: "a port-forward carries a connection to one pod; no name was resolved",
		}},
	}

	started := time.Now()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", local)
	if err != nil {
		// The tunnel is up and the container did not accept a connection on
		// that port. An ordinary answer, and the one somebody presses this to
		// find out.
		observation.Elapsed = time.Since(started)
		observation.Steps = append(observation.Steps, domain.ProbeStep{
			Name:   domain.StepConnect,
			Status: domain.StatusFailed,
			Detail: fmt.Sprintf("nothing accepted a connection on port %d in the pod", plan.Port),
		})
		return observation, nil
	}
	_ = conn.Close()

	observation.Steps = append(observation.Steps, domain.ProbeStep{
		Name:   domain.StepConnect,
		Status: domain.StatusOK,
		Detail: fmt.Sprintf("connected through an ephemeral forward on local port %d", forward.LocalPort),
	})

	if plan.HTTP() {
		step, code := probeHTTP(ctx, fmt.Sprintf("%s://%s/", plan.Scheme, local), plan.Timeout)
		observation.Steps = append(observation.Steps, step)
		observation.StatusCode = code
	}

	observation.Elapsed = time.Since(started)
	return observation, nil
}

// probeHTTP makes one request and reports what came back.
//
// Redirects are NOT followed and TLS is NOT verified, both deliberately. A
// redirect is an answer — it means something is serving — and following it
// would send this probe somewhere the operator did not name. A certificate
// cannot match "127.0.0.1" through a tunnel, so verifying it would fail every
// https probe for a reason that has nothing to do with reachability; the
// certificate itself is a different question, and CertificateInspector is
// where it is asked.
func probeHTTP(ctx context.Context, url string, timeout time.Duration) (domain.ProbeStep, int) {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			//nolint:gosec // See the doc comment: the name can never match through a tunnel.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return domain.ProbeStep{Name: domain.StepHTTP, Status: domain.StatusSkipped, Detail: err.Error()}, 0
	}

	response, err := client.Do(request)
	if err != nil {
		return domain.ProbeStep{
			Name:   domain.StepHTTP,
			Status: domain.StatusFailed,
			Detail: "the port accepted a connection but did not answer as HTTP",
		}, 0
	}
	defer func() { _ = response.Body.Close() }()

	return domain.ProbeStep{
		Name:   domain.StepHTTP,
		Status: domain.StatusOK,
		Detail: response.Status,
	}, response.StatusCode
}

// ProbeFromPod runs one bounded probe inside a container. See
// ports.InspectPort.
func (a *Adapter) ProbeFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	op := fmt.Sprintf("probing %s from %s/%s in %q", plan.Address(), namespace, podName, id)

	stdout := newCappedBuffer(probeOutputCap)
	stderr := newCappedBuffer(stderrCap)

	started := time.Now()
	err := a.runCommand(ctx, id, namespace, podName, containerName, domain.ProbeCommand(plan), nil, stdout, stderr)
	elapsed := time.Since(started)

	// A container with no shell at all fails before the script can say so, and
	// it is the same fact by a different route: there is nothing in this image
	// to probe with.
	if err != nil && shellMissing(err, stderr.String()) {
		return domain.ProbeObservation{}, fmt.Errorf("%s: %w: %s",
			op, ports.ErrProbeToolMissing, excerpt(firstNonEmpty(strings.TrimSpace(stderr.String()), err.Error()), stderrExcerpt))
	}
	if err != nil {
		// Everything else — cancelled, forbidden, the pod gone — goes through
		// the same classification a file copy's exec does, so an operator
		// reading it gets the sentence they would get anywhere else.
		return domain.ProbeObservation{}, a.commandOutcome(ctx, op, err, stderr.String())
	}

	observation, ok, parseErr := domain.ParseProbeOutput(stdout.String(), elapsed)
	if !ok {
		if errors.Is(parseErr, domain.ErrProbeOutputUnreadable) {
			return domain.ProbeObservation{}, fmt.Errorf("%s: %w", op, parseErr)
		}
		// The script's own "unsupported" line: it found no nc, curl or wget.
		return domain.ProbeObservation{}, fmt.Errorf("%s: %w: %s", op, ports.ErrProbeToolMissing, parseErr)
	}

	return observation, nil
}

// shellMissing reports a container with no /bin/sh, which the runtimes
// describe in as many different ways as they describe a missing tar — hence
// the same shape of test as tarMissing, and for the same reason: the
// alternative is an operator reading "exit status 127" and guessing.
func shellMissing(err error, stderr string) bool {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "no such file or directory") && strings.Contains(lower, "sh") {
		return true
	}

	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "executable file not found") ||
		(strings.Contains(message, "/bin/sh") && strings.Contains(message, "no such file")) {
		return true
	}

	var exit utilexec.ExitError
	return errors.As(err, &exit) && exit.ExitStatus() == 126
}
