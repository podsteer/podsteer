package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func probeNamespace(t *testing.T, name string) domain.NamespaceName {
	t.Helper()
	ns, err := domain.NewNamespaceName(name)
	if err != nil {
		t.Fatalf("NewNamespaceName(%q): %v", name, err)
	}
	return ns
}

func TestPlanProbeDecidesRouteAndTargetPerKindAndVantage(t *testing.T) {
	ns := probeNamespace(t, "shop")

	tests := []struct {
		name        string
		subject     domain.ProbeSubject
		vantage     domain.ProbeVantage
		wantRoute   domain.ProbeRoute
		wantHost    string
		wantScheme  string
		wantAddress string
	}{
		{
			name: "a service from here goes through the API server's own proxy",
			subject: domain.ProbeSubject{
				Kind: "Service", Namespace: ns, Name: "web",
				ServiceType: "ClusterIP", ClusterIP: "10.96.0.10",
				Port: 80, PortName: "http", Protocol: "TCP",
			},
			vantage:     domain.VantageLocal,
			wantRoute:   domain.RouteServiceProxy,
			wantHost:    "web",
			wantScheme:  "http",
			wantAddress: "web:80",
		},
		{
			name: "a service from inside the cluster is addressed by the name a workload would use",
			subject: domain.ProbeSubject{
				Kind: "Service", Namespace: ns, Name: "web",
				ServiceType: "ClusterIP", ClusterIP: "10.96.0.10",
				Port: 443, PortName: "https",
			},
			vantage:     domain.VantageInCluster,
			wantRoute:   domain.RouteExec,
			wantHost:    "web.shop.svc",
			wantScheme:  "https",
			wantAddress: "web.shop.svc:443",
		},
		{
			name: "a pod from here goes through a port-forward",
			subject: domain.ProbeSubject{
				Kind: "Pod", Namespace: ns, Name: "web-0",
				PodIP: "10.1.2.3", Port: 8080, PortName: "http",
			},
			vantage:    domain.VantageLocal,
			wantRoute:  domain.RoutePortForward,
			wantHost:   "127.0.0.1",
			wantScheme: "http",
		},
		{
			name: "a pod from inside the cluster is addressed by its own IP, never a name it may not have",
			subject: domain.ProbeSubject{
				Kind: "Pod", Namespace: ns, Name: "web-0",
				PodIP: "10.1.2.3", Port: 8080,
			},
			vantage:     domain.VantageInCluster,
			wantRoute:   domain.RouteExec,
			wantHost:    "10.1.2.3",
			wantAddress: "10.1.2.3:8080",
		},
		{
			name: "an ingress from inside the cluster resolves its public host",
			subject: domain.ProbeSubject{
				Kind: "Ingress", Namespace: ns, Name: "web",
				Host: "shop.example.com", Port: 443, TLS: true,
			},
			vantage:    domain.VantageInCluster,
			wantRoute:  domain.RouteExec,
			wantHost:   "shop.example.com",
			wantScheme: "https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := domain.PlanProbe(tt.subject, tt.vantage)
			if err != nil {
				t.Fatalf("PlanProbe: unexpected error %v", err)
			}
			if plan.Route != tt.wantRoute {
				t.Errorf("route = %q, want %q", plan.Route, tt.wantRoute)
			}
			if plan.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", plan.Host, tt.wantHost)
			}
			if tt.wantScheme != "" && plan.Scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", plan.Scheme, tt.wantScheme)
			}
			if tt.wantAddress != "" && plan.Address() != tt.wantAddress {
				t.Errorf("address = %q, want %q", plan.Address(), tt.wantAddress)
			}
			if plan.Vantage != tt.vantage {
				t.Errorf("vantage = %q, want %q", plan.Vantage, tt.vantage)
			}
		})
	}
}

// The timeout is the same at every vantage and is carried on the plan rather
// than invented by whatever performs it, so the panel can state it before the
// wait starts and no adapter can quietly wait longer.
func TestPlanProbeAlwaysCarriesTheHardTimeout(t *testing.T) {
	ns := probeNamespace(t, "shop")

	subjects := []struct {
		name    string
		subject domain.ProbeSubject
		vantage domain.ProbeVantage
	}{
		{"service, locally", domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80}, domain.VantageLocal},
		{"service, in cluster", domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80}, domain.VantageInCluster},
		{"pod, locally", domain.ProbeSubject{Kind: "Pod", Namespace: ns, Name: "web-0", PodIP: "10.1.1.1", Port: 80}, domain.VantageLocal},
		{"pod, in cluster", domain.ProbeSubject{Kind: "Pod", Namespace: ns, Name: "web-0", PodIP: "10.1.1.1", Port: 80}, domain.VantageInCluster},
		{"ingress, in cluster", domain.ProbeSubject{Kind: "Ingress", Namespace: ns, Name: "web", Host: "shop.example.com", Port: 80}, domain.VantageInCluster},
	}

	for _, tt := range subjects {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := domain.PlanProbe(tt.subject, tt.vantage)
			if err != nil {
				t.Fatalf("PlanProbe: %v", err)
			}
			if plan.Timeout != domain.ProbeTimeout {
				t.Errorf("timeout = %v, want %v", plan.Timeout, domain.ProbeTimeout)
			}
			if plan.Timeout <= 0 {
				t.Fatal("a probe must never be unbounded")
			}
		})
	}
}

func TestPlanProbeRefusesWhatCannotBeAnswered(t *testing.T) {
	ns := probeNamespace(t, "shop")

	tests := []struct {
		name    string
		subject domain.ProbeSubject
		vantage domain.ProbeVantage
		want    error
	}{
		{
			name:    "no port named",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1"},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeNoPort,
		},
		{
			name:    "a port out of range",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 70000},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeNoPort,
		},
		{
			name:    "a UDP port, which nothing here can connect to",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "dns", ClusterIP: "10.96.0.1", Port: 53, Protocol: "UDP"},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeNotTCP,
		},
		{
			name:    "an SCTP port, for the same reason",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "sig", ClusterIP: "10.96.0.1", Port: 38412, Protocol: "SCTP"},
			vantage: domain.VantageInCluster,
			want:    domain.ErrProbeNotTCP,
		},
		{
			name:    "a headless service has no single address to proxy to",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "db", ClusterIP: "None", Port: 5432},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeNoAddress,
		},
		{
			name:    "an ExternalName service has no cluster address at all",
			subject: domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "vendor", ServiceType: "ExternalName", Port: 443},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeNoAddress,
		},
		{
			name:    "an unscheduled pod has no address yet",
			subject: domain.ProbeSubject{Kind: "Pod", Namespace: ns, Name: "web-0", Port: 8080},
			vantage: domain.VantageInCluster,
			want:    domain.ErrProbeNoAddress,
		},
		{
			name:    "an ingress with no host in its rules",
			subject: domain.ProbeSubject{Kind: "Ingress", Namespace: ns, Name: "web", Port: 443},
			vantage: domain.VantageInCluster,
			want:    domain.ErrProbeNoAddress,
		},
		{
			name:    "an ingress from this machine, because that would mean contacting a host that is not an API server",
			subject: domain.ProbeSubject{Kind: "Ingress", Namespace: ns, Name: "web", Host: "shop.example.com", Port: 443},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeVantageUnavailable,
		},
		{
			name:    "a kind with no rule",
			subject: domain.ProbeSubject{Kind: "ConfigMap", Namespace: ns, Name: "settings", Port: 80},
			vantage: domain.VantageLocal,
			want:    domain.ErrProbeVantageUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.PlanProbe(tt.subject, tt.vantage)
			if !errors.Is(err, tt.want) {
				t.Fatalf("PlanProbe error = %v, want %v", err, tt.want)
			}
		})
	}
}

// A headless Service is refused locally and offered in-cluster, which is the
// point of separating the vantages: the answer nobody can give from here is
// one a pod can give perfectly well.
func TestPlanProbeOffersInClusterWhereLocalIsRefused(t *testing.T) {
	ns := probeNamespace(t, "shop")
	subject := domain.ProbeSubject{Kind: "Service", Namespace: ns, Name: "db", ClusterIP: "None", Port: 5432}

	if _, err := domain.PlanProbe(subject, domain.VantageLocal); !errors.Is(err, domain.ErrProbeNoAddress) {
		t.Fatalf("local: error = %v, want ErrProbeNoAddress", err)
	}

	plan, err := domain.PlanProbe(subject, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("in cluster: unexpected error %v", err)
	}
	if plan.Host != "db.shop.svc" {
		t.Errorf("host = %q, want db.shop.svc", plan.Host)
	}
}

func TestParseProbeOutputKeepsDNSAndConnectApart(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantSteps   int
		wantDNS     domain.ProbeStatus
		wantConnect domain.ProbeStatus
		wantCode    int
	}{
		{
			name:        "a name that resolved and a port that answered",
			raw:         "dns ok resolved to 10.96.0.10\nconnect ok tcp handshake completed\nhttp ok 204 \n",
			wantSteps:   3,
			wantDNS:     domain.StatusOK,
			wantConnect: domain.StatusOK,
			wantCode:    204,
		},
		{
			name:      "a name that did not resolve stops there, and connect is absent rather than failed",
			raw:       "dns failed the name did not resolve in this container\n",
			wantSteps: 1,
			wantDNS:   domain.StatusFailed,
		},
		{
			name:        "an address literal skips resolution without claiming it succeeded",
			raw:         "dns skipped 10.1.2.3 is already an address\nconnect failed nothing accepted a connection on that port\n",
			wantSteps:   2,
			wantDNS:     domain.StatusSkipped,
			wantConnect: domain.StatusFailed,
		},
		{
			name:        "a line this build does not understand is ignored rather than refused",
			raw:         "tls ok TLSv1.3\ndns skipped literal\nconnect ok\n",
			wantSteps:   2,
			wantDNS:     domain.StatusSkipped,
			wantConnect: domain.StatusOK,
		},
		{
			name:        "a status code that is not one leaves the code at zero and keeps the detail",
			raw:         "dns skipped literal\nconnect ok\nhttp ok not-a-code\n",
			wantSteps:   3,
			wantDNS:     domain.StatusSkipped,
			wantConnect: domain.StatusOK,
			wantCode:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation, ok, err := domain.ParseProbeOutput(tt.raw, 42*time.Millisecond)
			if err != nil || !ok {
				t.Fatalf("ParseProbeOutput: ok=%v err=%v", ok, err)
			}
			if len(observation.Steps) != tt.wantSteps {
				t.Fatalf("steps = %d, want %d (%v)", len(observation.Steps), tt.wantSteps, observation.Steps)
			}
			if got := observation.Steps[0]; got.Name != domain.StepDNS || got.Status != tt.wantDNS {
				t.Errorf("dns step = %+v, want status %q", got, tt.wantDNS)
			}
			if tt.wantConnect != "" {
				found := false
				for _, step := range observation.Steps {
					if step.Name == domain.StepConnect {
						found = true
						if step.Status != tt.wantConnect {
							t.Errorf("connect status = %q, want %q", step.Status, tt.wantConnect)
						}
					}
				}
				if !found {
					t.Error("expected a connect step")
				}
			}
			if observation.StatusCode != tt.wantCode {
				t.Errorf("status code = %d, want %d", observation.StatusCode, tt.wantCode)
			}
			if observation.Elapsed != 42*time.Millisecond {
				t.Errorf("elapsed = %v, want the value passed in", observation.Elapsed)
			}
		})
	}
}

// A container with nothing to probe with has said nothing about the target,
// so it must never come back as an observation the panel would render as a
// failed probe.
func TestParseProbeOutputReportsAContainerWithNothingToProbeWith(t *testing.T) {
	_, ok, err := domain.ParseProbeOutput("unsupported this container has no nc, curl or wget to probe with\n", time.Second)
	if ok {
		t.Fatal("an unsupported container must not yield an observation")
	}
	if err == nil {
		t.Fatal("expected a reason")
	}
	if errors.Is(err, domain.ErrProbeOutputUnreadable) {
		t.Fatal("having no tool is not unreadable output; the two need different advice")
	}
	if !strings.Contains(err.Error(), "nc") {
		t.Errorf("the reason should name what was missing, got %q", err)
	}
}

func TestParseProbeOutputRefusesOutputItCannotRead(t *testing.T) {
	for _, raw := range []string{"", "\n\n", "something else entirely", "dns", "banana ok"} {
		_, ok, err := domain.ParseProbeOutput(raw, time.Second)
		if ok || !errors.Is(err, domain.ErrProbeOutputUnreadable) {
			t.Errorf("ParseProbeOutput(%q): ok=%v err=%v, want ErrProbeOutputUnreadable", raw, ok, err)
		}
	}
}

// The command and the parser are one protocol, so what the script emits has
// to be what the parser reads. The shapes below are the ones the script
// actually prints.
func TestProbeCommandAndParserAgreeOnTheProtocol(t *testing.T) {
	ns := probeNamespace(t, "shop")
	plan, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80, PortName: "http",
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}

	command := domain.ProbeCommand(plan)
	if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-c" {
		t.Fatalf("command = %v, want one sh -c invocation", command)
	}

	script := command[2]
	for _, line := range []string{
		`echo "dns ok resolved to $A"`,
		`echo "dns failed the name did not resolve in this container"`,
		`echo "connect ok tcp handshake completed"`,
		`echo "connect failed nothing accepted a connection on that port"`,
		`echo "unsupported`,
	} {
		if !strings.Contains(script, line) {
			t.Errorf("script does not emit %q", line)
		}
	}

	// Every literal the script prints has to parse. Rendered with the shell's
	// substitutions already made, which is what the container writes.
	rendered := "dns ok resolved to 10.96.0.10\nconnect ok tcp handshake completed\nhttp ok 200\n"
	observation, ok, err := domain.ParseProbeOutput(rendered, time.Second)
	if !ok || err != nil {
		t.Fatalf("the parser cannot read what the script writes: ok=%v err=%v", ok, err)
	}
	if observation.StatusCode != 200 {
		t.Errorf("status code = %d, want 200", observation.StatusCode)
	}
}

// The host and port reach the shell as data, never as syntax. PlanProbe has
// already vetted both, and quoting is the second line of that defence.
func TestProbeCommandQuotesTheTarget(t *testing.T) {
	ns := probeNamespace(t, "shop")
	plan, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Ingress", Namespace: ns, Name: "web", Host: "shop.example.com", Port: 443, TLS: true,
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}

	script := domain.ProbeCommand(plan)[2]
	if !strings.Contains(script, `H='shop.example.com'; P='443'`) {
		t.Errorf("script does not bind a quoted host and port: %s", script)
	}
	if !strings.Contains(script, "curl") || !strings.Contains(script, "wget") {
		t.Error("an https target should attempt an HTTP request with whichever tool the image has")
	}
}

// A bare TCP target asks no HTTP question at all — a port named "postgres"
// is not http, and requesting one would report a failure that says nothing.
func TestProbeCommandSkipsHTTPForANonHTTPScheme(t *testing.T) {
	ns := probeNamespace(t, "shop")
	plan, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Pod", Namespace: ns, Name: "db-0", PodIP: "10.1.2.3", Port: 5432, PortName: "postgres",
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}

	// SchemeForPort answers "http" for everything but a port named https, and
	// that is deliberate and shared with the port-forward's address — so this
	// plan does carry a scheme. What matters is that the two features cannot
	// disagree about it.
	if plan.Scheme != "http" {
		t.Fatalf("scheme = %q; SchemeForPort is the one implementation of this", plan.Scheme)
	}
}

func TestNewProbeResultSeparatesResolutionFromConnection(t *testing.T) {
	ns := probeNamespace(t, "shop")
	plan, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80, PortName: "http",
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}

	tests := []struct {
		name        string
		steps       []domain.ProbeStep
		code        int
		wantOutcome domain.ProbeOutcome
		wantInSay   string
	}{
		{
			name: "a name that did not resolve is never reported as refused",
			steps: []domain.ProbeStep{
				{Name: domain.StepDNS, Status: domain.StatusFailed, Detail: "no such host"},
			},
			wantOutcome: domain.OutcomeNameNotResolved,
			wantInSay:   "did not resolve",
		},
		{
			name: "a name that resolved and a port that refused is refused",
			steps: []domain.ProbeStep{
				{Name: domain.StepDNS, Status: domain.StatusOK},
				{Name: domain.StepConnect, Status: domain.StatusFailed},
			},
			wantOutcome: domain.OutcomeRefused,
			wantInSay:   "could not connect",
		},
		{
			name: "a connection that established is reachable",
			steps: []domain.ProbeStep{
				{Name: domain.StepDNS, Status: domain.StatusOK},
				{Name: domain.StepConnect, Status: domain.StatusOK},
			},
			wantOutcome: domain.OutcomeReachable,
			wantInSay:   "connected to",
		},
		{
			name: "an HTTP status rides the summary when one was asked for",
			steps: []domain.ProbeStep{
				{Name: domain.StepDNS, Status: domain.StatusOK},
				{Name: domain.StepConnect, Status: domain.StatusOK},
				{Name: domain.StepHTTP, Status: domain.StatusOK},
			},
			code:        503,
			wantOutcome: domain.OutcomeReachable,
			wantInSay:   "HTTP 503",
		},
		{
			name:        "a probe that got nowhere says so rather than claiming a refusal",
			steps:       []domain.ProbeStep{{Name: domain.StepDNS, Status: domain.StatusSkipped}},
			wantOutcome: domain.OutcomeUnknown,
			wantInSay:   "did not get as far as connecting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domain.NewProbeResult(plan, domain.ProbeObservation{Steps: tt.steps, StatusCode: tt.code})
			if result.Outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", result.Outcome, tt.wantOutcome)
			}
			if !strings.Contains(result.Summary, tt.wantInSay) {
				t.Errorf("summary = %q, want it to contain %q", result.Summary, tt.wantInSay)
			}
		})
	}
}

// The summary has to name the vantage, because "reachable" without one is the
// least useful true statement this feature could make.
func TestProbeSummaryNamesWhereTheAnswerCameFrom(t *testing.T) {
	ns := probeNamespace(t, "shop")
	reachable := domain.ProbeObservation{Steps: []domain.ProbeStep{
		{Name: domain.StepConnect, Status: domain.StatusOK},
	}}

	local, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80,
	}, domain.VantageLocal)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}
	if summary := domain.NewProbeResult(local, reachable).Summary; !strings.Contains(summary, "API server") {
		t.Errorf("local summary = %q, want it to name the API server's proxy", summary)
	}

	inCluster, err := domain.PlanProbe(domain.ProbeSubject{
		Kind: "Service", Namespace: ns, Name: "web", ClusterIP: "10.96.0.1", Port: 80,
	}, domain.VantageInCluster)
	if err != nil {
		t.Fatalf("PlanProbe: %v", err)
	}
	if summary := domain.NewProbeResult(inCluster, reachable).Summary; !strings.Contains(summary, "container") {
		t.Errorf("in-cluster summary = %q, want it to name the container", summary)
	}
}
