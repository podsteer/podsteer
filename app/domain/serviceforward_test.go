package domain_test

import (
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestSelectServicePortAnswersTheObviousCaseAndRefusesTheAmbiguousOne is the
// whole of this function's judgement.
//
// Forwarding to the wrong port of a multi-port Service produces a connection
// that establishes and then behaves like a broken application — a long way to
// travel before finding out — so a Service with three ports and no choice
// made is a question rather than a guess.
func TestSelectServicePortAnswersTheObviousCaseAndRefusesTheAmbiguousOne(t *testing.T) {
	t.Parallel()

	single := []domain.ServicePort{{Name: "", Port: 80, TargetPort: "8080", Protocol: "TCP"}}
	chosen, err := domain.SelectServicePort(single, "")
	if err != nil {
		t.Fatalf("SelectServicePort() error = %v for a single-port Service", err)
	}
	if chosen.Port != 80 {
		t.Errorf("chose port %d, want 80", chosen.Port)
	}

	several := []domain.ServicePort{
		{Name: "http", Port: 80, TargetPort: "http"},
		{Name: "metrics", Port: 9090, TargetPort: "metrics"},
	}
	if _, err := domain.SelectServicePort(several, ""); !errors.Is(err, domain.ErrServicePortAmbiguous) {
		t.Fatalf("error = %v, want the refusal to choose", err)
	}
}

// TestSelectServicePortTakesANumberOrAName covers both ways somebody names a
// port, and the case where a Service has named one after a number.
func TestSelectServicePortTakesANumberOrAName(t *testing.T) {
	t.Parallel()

	ports := []domain.ServicePort{
		{Name: "http", Port: 80, TargetPort: "http"},
		{Name: "metrics", Port: 9090, TargetPort: "metrics"},
		// A port NAMED "8080" that listens on 8081. The number is tried
		// first, so typing 8080 here reaches neither by accident: no port
		// LISTENS on 8080, and the name then matches.
		{Name: "8080", Port: 8081, TargetPort: "odd"},
	}

	for _, want := range []struct {
		asked string
		port  int
	}{
		{"80", 80},
		{"http", 80},
		{"9090", 9090},
		{"metrics", 9090},
		{"8080", 8081},
	} {
		chosen, err := domain.SelectServicePort(ports, want.asked)
		if err != nil {
			t.Fatalf("SelectServicePort(%q) error = %v", want.asked, err)
		}
		if chosen.Port != want.port {
			t.Errorf("SelectServicePort(%q) = %d, want %d", want.asked, chosen.Port, want.port)
		}
	}

	if _, err := domain.SelectServicePort(ports, "nope"); !errors.Is(err, domain.ErrServicePortNotFound) {
		t.Fatalf("error = %v, want not-found", err)
	}
}

// TestResolveTargetPortHandlesTheThreeShapesKubernetesAllows, of which the
// middle one is the trap: an ABSENT targetPort defaults to the service port
// and is not zero.
func TestResolveTargetPortHandlesTheThreeShapesKubernetesAllows(t *testing.T) {
	t.Parallel()

	containers := []domain.ContainerPort{{Name: "http", Port: 8080}, {Name: "admin", Port: 9000}}

	numbered, err := domain.ResolveTargetPort(domain.ServicePort{Port: 80, TargetPort: "8080"}, containers)
	if err != nil || numbered != 8080 {
		t.Fatalf("numbered target = %d, %v; want 8080", numbered, err)
	}

	absent, err := domain.ResolveTargetPort(domain.ServicePort{Port: 5432, TargetPort: ""}, containers)
	if err != nil {
		t.Fatalf("absent target error = %v", err)
	}
	if absent != 5432 {
		t.Fatalf("absent target = %d, want the service port 5432 — an unset targetPort is not zero", absent)
	}

	named, err := domain.ResolveTargetPort(domain.ServicePort{Port: 80, TargetPort: "http"}, containers)
	if err != nil || named != 8080 {
		t.Fatalf("named target = %d, %v; want 8080", named, err)
	}
}

// TestResolveTargetPortRefusesANameThePodDoesNotDeclare rather than guessing.
//
// A named targetPort means something only against a particular pod, and a pod
// that does not declare it is a real misconfiguration — one the operator has
// to see rather than have papered over with the service port, which would
// forward to a port nothing is listening on.
func TestResolveTargetPortRefusesANameThePodDoesNotDeclare(t *testing.T) {
	t.Parallel()

	_, err := domain.ResolveTargetPort(
		domain.ServicePort{Port: 80, TargetPort: "http"},
		[]domain.ContainerPort{{Name: "web", Port: 8080}},
	)
	if !errors.Is(err, domain.ErrTargetPortUnresolved) {
		t.Fatalf("error = %v, want the unresolved-name refusal", err)
	}
}
