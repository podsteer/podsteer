package wails

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// A container with nothing to probe with is NOT an answer about the target,
// so it gets its own code — the sibling of tar_missing, and for the same
// reason. Reported as unreachable it would draw a red cross against somebody
// else's Service on the strength of this image.
func TestAContainerWithNothingToProbeWithHasItsOwnCode(t *testing.T) {
	code, message := classifyError(fmt.Errorf("probing: %w: no nc", ports.ErrProbeToolMissing))

	if code != CodeProbeToolMissing {
		t.Fatalf("code = %q, want %q", code, CodeProbeToolMissing)
	}
	if !strings.Contains(message, "says nothing about whether the target is reachable") {
		t.Errorf("message = %q, want it to disclaim any verdict about the target", message)
	}
	if code == CodeUnreachable {
		t.Fatal("this must never classify as the cluster being unreachable")
	}
}

// The refusals PlanProbe makes are facts about the object that stay true
// however many times the button is pressed, so they carry their own sentence
// and their own code — the panel renders them where the result would have
// gone rather than as a failure.
func TestProbeRefusalsCarryTheirOwnReasonVerbatim(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a UDP port",
			err:  fmt.Errorf("shop/dns port 53 is UDP: %w", domain.ErrProbeNotTCP),
			want: "port 53 is UDP",
		},
		{
			name: "a headless service",
			err:  fmt.Errorf("db is headless and has no single address to reach: %w", domain.ErrProbeNoAddress),
			want: "headless",
		},
		{
			name: "an ingress host, which is not something PodSteer connects to",
			err:  fmt.Errorf("reaching shop.example.com would mean connecting to a host that is not an API server, which PodSteer does not do: %w", domain.ErrProbeVantageUnavailable),
			want: "not an API server",
		},
		{
			name: "output nothing could read",
			err:  fmt.Errorf("probing: %w", domain.ErrProbeOutputUnreadable),
			want: "no readable result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, message := classifyError(tt.err)
			if code != CodeProbeUnavailable {
				t.Errorf("code = %q, want %q", code, CodeProbeUnavailable)
			}
			if !strings.Contains(message, tt.want) {
				t.Errorf("message = %q, want it to contain %q", message, tt.want)
			}
		})
	}
}

// A read-only cluster refusing an in-cluster probe classifies as read_only,
// never forbidden: there is nobody to ask for a permission, and the fix is in
// Organise on this machine.
func TestAReadOnlyRefusalOfAProbeIsNotForbidden(t *testing.T) {
	code, _ := classifyError(fmt.Errorf(`cluster "staging": %w`, ports.ErrReadOnly))
	if code != CodeReadOnly {
		t.Fatalf("code = %q, want %q", code, CodeReadOnly)
	}
}

func TestToProbeResultCarriesTheVantageTheRouteAndEveryStep(t *testing.T) {
	ns, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("NewNamespaceName: %v", err)
	}

	plan, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web",
		ClusterIP: "10.96.0.10", Port: 80, PortName: "http",
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}

	result := domain.NewProbeResult(plan, domain.ProbeObservation{
		Steps: []domain.ProbeStep{
			{Name: domain.StepDNS, Status: domain.StatusOK, Detail: "resolved to 10.96.0.10"},
			{Name: domain.StepConnect, Status: domain.StatusOK},
			{Name: domain.StepHTTP, Status: domain.StatusOK},
		},
		StatusCode: 200,
	})

	dto := toProbeResult(result)
	if dto.Vantage != string(domain.VantageInCluster) {
		t.Errorf("vantage = %q, want in_cluster — the panel has to be able to say which", dto.Vantage)
	}
	if dto.Route != string(domain.RouteExec) {
		t.Errorf("route = %q, want exec", dto.Route)
	}
	if dto.Target != "web.shop.svc:80" {
		t.Errorf("target = %q", dto.Target)
	}
	if len(dto.Steps) != 3 {
		t.Fatalf("steps = %d, want every one carried across", len(dto.Steps))
	}
	if dto.Steps[0].Name != "dns" || dto.Steps[1].Name != "connect" {
		t.Errorf("steps = %v, want dns and connect kept apart on the wire too", dto.Steps)
	}
	if dto.TimeoutMs != domain.ProbeTimeout.Milliseconds() {
		t.Errorf("timeoutMs = %d, want the ceiling stated", dto.TimeoutMs)
	}
}

func TestToImageReportNormalisesEmptyListsAndCarriesTheNotes(t *testing.T) {
	report := toImageReport(domain.NewImageReport(domain.ImageFacts{
		Container:   "app",
		ResolvedRef: "ghcr.io/team/app:v1",
		PullSecrets: []string{"ghcr-pull"},
	}))

	if report.OtherNames == nil || report.PullSecrets == nil {
		t.Fatal("a null array is one more branch every consumer has to remember")
	}
	if report.Bounded == "" {
		t.Fatal("the bounded line is not optional")
	}
	if !report.Credentialed || report.CredentialNote == "" {
		t.Fatal("an image pulled with credentials has to say what was not read")
	}
	if report.Registry != "ghcr.io" || report.Tag != "v1" {
		t.Errorf("reference = %s/%s:%s", report.Registry, report.Repository, report.Tag)
	}
}

// Nothing on this path may read a Secret, so the report has no field that
// could carry one — and the note says so in words rather than by omission.
func TestAnImageReportHasNowhereToPutASecretValue(t *testing.T) {
	report := toImageReport(domain.NewImageReport(domain.ImageFacts{
		Container:   "app",
		ResolvedRef: "ghcr.io/team/private:v1",
		PullSecrets: []string{"ghcr-pull"},
	}))

	if !strings.Contains(report.CredentialNote, "does not read that Secret") {
		t.Errorf("credential note = %q", report.CredentialNote)
	}
	for _, secret := range report.PullSecrets {
		if secret != "ghcr-pull" {
			t.Errorf("pull secret = %q, want the name only", secret)
		}
	}
}

func TestNewInspectAPIRejectsMissingDependencies(t *testing.T) {
	if _, err := NewInspectAPI(nil, &App{}, nil); err == nil {
		t.Error("NewInspectAPI() succeeded without a service")
	}
	if _, err := NewInspectAPI(stubInspectService{}, nil, nil); err == nil {
		t.Error("NewInspectAPI() succeeded without an App")
	}
}

type stubInspectService struct{}

func (stubInspectService) ProbeFromHere(_ context.Context, _ domain.ClusterID, _ domain.ProbeSubject) (domain.ProbeResult, error) {
	return domain.ProbeResult{}, errors.New("not called")
}

func (stubInspectService) ProbeFromPod(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, _, _ string, _ domain.ProbeSubject) (domain.ProbeResult, error) {
	return domain.ProbeResult{}, errors.New("not called")
}

func (stubInspectService) ImageReport(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, _, _ string) (domain.ImageReport, error) {
	return domain.ImageReport{}, errors.New("not called")
}
