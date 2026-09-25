package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeInspectPort records what it was asked and answers whatever the test set.
type fakeInspectPort struct {
	fromHereCalled bool
	fromPodCalled  bool
	factsCalled    bool

	// lastPlan is the plan the port was handed, which is how a test asserts
	// what the domain decided without reaching into it.
	lastPlan  domain.ProbePlan
	lastPod   string
	lastCntr  string
	observed  domain.ProbeObservation
	probeErr  error
	facts     domain.ImageFacts
	factsErr  error
	blockFor  time.Duration
	ctxErrOut error
}

func (f *fakeInspectPort) ProbeFromHere(ctx context.Context, _ domain.ClusterID, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	f.fromHereCalled = true
	f.lastPlan = plan
	if f.blockFor > 0 {
		select {
		case <-time.After(f.blockFor):
		case <-ctx.Done():
			f.ctxErrOut = ctx.Err()
			return domain.ProbeObservation{}, ctx.Err()
		}
	}
	return f.observed, f.probeErr
}

func (f *fakeInspectPort) ProbeFromPod(ctx context.Context, _ domain.ClusterID, _ domain.NamespaceName, podName, containerName string, plan domain.ProbePlan) (domain.ProbeObservation, error) {
	f.fromPodCalled = true
	f.lastPlan = plan
	f.lastPod = podName
	f.lastCntr = containerName
	if f.blockFor > 0 {
		select {
		case <-time.After(f.blockFor):
		case <-ctx.Done():
			f.ctxErrOut = ctx.Err()
			return domain.ProbeObservation{}, ctx.Err()
		}
	}
	return f.observed, f.probeErr
}

func (f *fakeInspectPort) ImageFacts(context.Context, domain.ClusterID, domain.NamespaceName, string, string) (domain.ImageFacts, error) {
	f.factsCalled = true
	return f.facts, f.factsErr
}

func newInspectService(t *testing.T, inspect *fakeInspectPort, registry *application.Registry, logger *slog.Logger) *application.InspectService {
	t.Helper()

	service, err := application.NewInspectService(application.InspectServiceDeps{
		Inspect:  inspect,
		Registry: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewInspectService() error = %v", err)
	}
	return service
}

func serviceSubject(t *testing.T) domain.ProbeSubject {
	t.Helper()
	ns, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("NewNamespaceName: %v", err)
	}
	return domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web",
		ServiceType: "ClusterIP", ClusterIP: "10.96.0.10",
		Port: 80, PortName: "http", Protocol: "TCP",
	}
}

func TestNewInspectServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := application.NewInspectService(application.InspectServiceDeps{}); err == nil {
		t.Error("NewInspectService() succeeded without an InspectPort, want an error")
	}
	if _, err := application.NewInspectService(application.InspectServiceDeps{Inspect: &fakeInspectPort{}}); err == nil {
		t.Error("NewInspectService() succeeded without a Registry, want an error")
	}
}

// THE GUARD. An in-cluster probe runs a command in somebody's container, which
// is write-shaped whatever it reads, so it is refused on a cluster the
// operator marked read-only — and refused BEFORE the port is reached, so the
// refusal costs nothing and cannot be mistaken for an answer about the target.
func TestProbeFromPodIsRefusedOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	id := domain.ClusterID("staging")
	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	inspect := &fakeInspectPort{}
	service := newInspectService(t, inspect, registry, nil)

	_, err := service.ProbeFromPod(context.Background(), id, "shop", "web-0", "app", serviceSubject(t))
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Fatalf("ProbeFromPod() error = %v, want ports.ErrReadOnly", err)
	}
	if inspect.fromPodCalled {
		t.Error("the exec reached the adapter on a read-only cluster")
	}
}

// The local probe opens a socket and closes it again; it changes nothing, so
// the guard has no business refusing it — the same line StreamLogs and
// DownloadFromPod sit on.
func TestProbeFromHereIsNotRefusedOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	id := domain.ClusterID("staging")
	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	inspect := &fakeInspectPort{observed: domain.ProbeObservation{
		Steps: []domain.ProbeStep{{Name: domain.StepConnect, Status: domain.StatusOK}},
	}}
	service := newInspectService(t, inspect, registry, nil)

	result, err := service.ProbeFromHere(context.Background(), id, serviceSubject(t))
	if err != nil {
		t.Fatalf("ProbeFromHere() error = %v", err)
	}
	if !inspect.fromHereCalled {
		t.Fatal("the probe never reached the adapter")
	}
	if result.Outcome != domain.OutcomeReachable {
		t.Errorf("outcome = %q, want reachable", result.Outcome)
	}
}

// An in-cluster probe leaves one line naming the cluster, namespace, pod,
// container and target — and NEVER what came back. The output is the
// container's answer about somebody else's Service, and the discipline that
// keeps a file transfer's audit line free of file contents applies here.
func TestProbeFromPodAuditsWhatWasRunAndNeverWhatCameBack(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	inspect := &fakeInspectPort{observed: domain.ProbeObservation{
		Steps: []domain.ProbeStep{
			{Name: domain.StepDNS, Status: domain.StatusOK, Detail: "resolved to 10.96.0.10"},
			{Name: domain.StepConnect, Status: domain.StatusOK, Detail: "tcp handshake completed"},
		},
		StatusCode: 418,
	}}
	service := newInspectService(t, inspect, application.NewRegistry(), logger)

	if _, err := service.ProbeFromPod(context.Background(), "prod", "shop", "web-0", "app", serviceSubject(t)); err != nil {
		t.Fatalf("ProbeFromPod() error = %v", err)
	}

	line := logged.String()
	for _, want := range []string{"cluster=prod", "namespace=shop", "pod=web-0", "container=app", "web.shop.svc:80"} {
		if !strings.Contains(line, want) {
			t.Errorf("audit line does not name %q: %s", want, line)
		}
	}
	for _, forbidden := range []string{"10.96.0.10", "418", "handshake"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("audit line carries the probe's output (%q): %s", forbidden, line)
		}
	}
}

// A probe that could not be performed is audited too, for the reason a
// refused write is recorded in the timeline: "I pressed it and nothing
// happened" is exactly the question a log answers.
func TestProbeFromPodAuditsAFailure(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	inspect := &fakeInspectPort{probeErr: ports.ErrProbeToolMissing}
	service := newInspectService(t, inspect, application.NewRegistry(), logger)

	if _, err := service.ProbeFromPod(context.Background(), "prod", "shop", "web-0", "app", serviceSubject(t)); !errors.Is(err, ports.ErrProbeToolMissing) {
		t.Fatalf("ProbeFromPod() error = %v, want ErrProbeToolMissing", err)
	}
	if !strings.Contains(logged.String(), "container=app") {
		t.Errorf("a failed probe is not audited: %s", logged.String())
	}
}

// A subject the domain refuses never reaches the adapter, at either vantage:
// the refusal is a fact about the object and costs no request.
func TestAProbeThatCannotBePlannedNeverReachesTheCluster(t *testing.T) {
	t.Parallel()

	ns, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("NewNamespaceName: %v", err)
	}
	udp := domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "dns", ClusterIP: "10.96.0.10", Port: 53, Protocol: "UDP"}

	inspect := &fakeInspectPort{}
	service := newInspectService(t, inspect, application.NewRegistry(), nil)

	if _, err := service.ProbeFromHere(context.Background(), "prod", udp); !errors.Is(err, domain.ErrProbeNotTCP) {
		t.Errorf("ProbeFromHere() error = %v, want ErrProbeNotTCP", err)
	}
	if _, err := service.ProbeFromPod(context.Background(), "prod", ns, "web-0", "app", udp); !errors.Is(err, domain.ErrProbeNotTCP) {
		t.Errorf("ProbeFromPod() error = %v, want ErrProbeNotTCP", err)
	}
	if inspect.fromHereCalled || inspect.fromPodCalled {
		t.Error("a refused plan reached the adapter anyway")
	}
}

// The plan the adapter receives carries the hard timeout, so no adapter has
// to invent one and none can quietly wait longer.
func TestTheAdapterAlwaysReceivesTheHardTimeout(t *testing.T) {
	t.Parallel()

	inspect := &fakeInspectPort{observed: domain.ProbeObservation{
		Steps: []domain.ProbeStep{{Name: domain.StepConnect, Status: domain.StatusOK}},
	}}
	service := newInspectService(t, inspect, application.NewRegistry(), nil)

	if _, err := service.ProbeFromHere(context.Background(), "prod", serviceSubject(t)); err != nil {
		t.Fatalf("ProbeFromHere() error = %v", err)
	}
	if inspect.lastPlan.Timeout != domain.ProbeTimeout {
		t.Errorf("plan timeout = %v, want %v", inspect.lastPlan.Timeout, domain.ProbeTimeout)
	}
}

// A caller's own deadline still ends a probe: the hard ceiling is the longest
// it can take, never the shortest.
func TestAProbeEndsWhenTheCallersContextDoes(t *testing.T) {
	t.Parallel()

	inspect := &fakeInspectPort{blockFor: 10 * time.Second}
	service := newInspectService(t, inspect, application.NewRegistry(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := service.ProbeFromHere(ctx, "prod", serviceSubject(t))
	if err == nil {
		t.Fatal("ProbeFromHere() ignored the caller's deadline")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("the probe waited %v past its context", elapsed)
	}
}

func TestImageReportShapesWhatTheAdapterGathered(t *testing.T) {
	t.Parallel()

	inspect := &fakeInspectPort{facts: domain.ImageFacts{
		Container:   "app",
		ResolvedRef: "ghcr.io/team/app:v1",
		NodeName:    "node-1",
		NodeImages:  []domain.NodeImage{{Names: []string{"ghcr.io/team/app:v1"}, SizeBytes: 99}},
	}}
	service := newInspectService(t, inspect, application.NewRegistry(), nil)

	report, err := service.ImageReport(context.Background(), "prod", "shop", "web-0", "app")
	if err != nil {
		t.Fatalf("ImageReport() error = %v", err)
	}
	if !inspect.factsCalled {
		t.Fatal("ImageReport never reached the adapter")
	}
	if report.SizeStatus != domain.ImageSizeMeasured || report.SizeBytes != 99 {
		t.Errorf("report = %+v, want the node's own figure", report)
	}
	if report.Bounded == "" {
		t.Error("the report must say what it did not look at")
	}
}

// An image report reads a pod and a node and changes nothing, so it is not
// guarded — an operator who marked a cluster read-only can still ask what an
// image is.
func TestImageReportIsNotRefusedOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	id := domain.ClusterID("staging")
	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	inspect := &fakeInspectPort{facts: domain.ImageFacts{Container: "app", ResolvedRef: "nginx"}}
	service := newInspectService(t, inspect, registry, nil)

	if _, err := service.ImageReport(context.Background(), id, "shop", "web-0", "app"); err != nil {
		t.Fatalf("ImageReport() error = %v", err)
	}
}
