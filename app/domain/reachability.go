package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// "Can this be reached" is two different questions wearing one word, and the
// answer is worthless without saying which was asked. A Service that answers
// through the API server's proxy tells you the endpoints are up; it tells you
// nothing about whether a pod in another namespace may talk to it, because
// the API server is not subject to the NetworkPolicy that pod is. A probe run
// inside a workload's own container exercises cluster DNS, the CNI and every
// policy in the path — and tells you nothing about whether YOU can reach it
// from this laptop.
//
// So every probe here carries its vantage, the route it took, and the steps it
// actually performed, and the UI is expected to say all three. A result that
// merely said "reachable" would be the least useful true statement available.
//
// Everything in this file is pure: planning decides what a probe would do,
// parsing turns what a probe wrote into facts, and shaping turns facts into a
// verdict. The adapter opens the sockets. That is the same plan-in-the-domain,
// execute-in-the-adapter split PlanDrain and PlanRolloutPromote follow, and it
// is why the rules below can be argued with in a test rather than observed
// against somebody's cluster.

// ProbeVantage is where a probe was performed from.
type ProbeVantage string

const (
	// VantageLocal is this machine, reaching the cluster the only way this
	// process ever reaches anything: through the API server named in the
	// kubeconfig.
	VantageLocal ProbeVantage = "local"
	// VantageInCluster is inside a container the operator chose, so cluster
	// DNS and NetworkPolicy are exercised as a workload experiences them.
	VantageInCluster ProbeVantage = "in_cluster"
)

// ProbeRoute is HOW a vantage reached the target — the thing that decides
// what the answer is evidence of.
type ProbeRoute string

const (
	// RouteServiceProxy is the API server's own service proxy. The connection
	// to the endpoint is made BY THE API SERVER, so a success says the
	// endpoints answer, not that anything else in the cluster may reach them.
	RouteServiceProxy ProbeRoute = "service_proxy"
	// RoutePortForward is an ephemeral port-forward this process opened and
	// tore down again: a real TCP connection from this machine, tunnelled
	// through the API server to one pod.
	RoutePortForward ProbeRoute = "port_forward"
	// RouteExec is a bounded command run in an existing container.
	RouteExec ProbeRoute = "exec"
)

// ProbeTimeout is the ceiling on any single probe, at every vantage.
//
// A hard bound rather than a setting. A probe is a question somebody pressed a
// button to ask and is standing there waiting for; a connect that has not
// completed in five seconds has already answered it, and a longer wait only
// makes an unreachable target indistinguishable from a hung application.
const ProbeTimeout = 5 * time.Second

// Sentinels for a probe that cannot be planned. Each is a fact about the
// object or the vantage rather than a failure, so the UI states it where the
// result would have gone rather than raising a dialog.
var (
	// ErrProbeNoPort means nothing named a port to probe.
	ErrProbeNoPort = errors.New("no port to probe")
	// ErrProbeNotTCP means the port is UDP or SCTP. Nothing here can carry
	// those: the API server's proxy is HTTP, a port-forward is TCP, and a
	// connect attempt to a UDP port that appeared to succeed would be
	// meaningless — UDP has nothing to accept.
	ErrProbeNotTCP = errors.New("only TCP ports can be probed")
	// ErrProbeNoAddress means the object has no address to aim at from this
	// vantage — a Service with no cluster IP, a pod that has not been
	// scheduled, an Ingress with no host.
	ErrProbeNoAddress = errors.New("no address to probe")
	// ErrProbeVantageUnavailable means this vantage cannot answer for this
	// kind at all, and no permission or setting changes that. It is the
	// Bounded-not-Unreadable distinction PodGraph already makes.
	ErrProbeVantageUnavailable = errors.New("that vantage cannot reach this object")
	// ErrProbeOutputUnreadable means a probe ran and wrote something this
	// build cannot read. Distinct from the target being unreachable, which is
	// an ordinary answer.
	ErrProbeOutputUnreadable = errors.New("the probe produced no readable result")
)

// ProbeSubject is what the frontend read off the object it is offering to
// probe — quotation, every field of it, so nothing here needs a cluster.
type ProbeSubject struct {
	// Kind is the Kubernetes Kind verbatim: "Service", "Pod" or "Ingress".
	Kind string
	// Namespace and Name identify the object.
	Namespace NamespaceName
	Name      string
	// ServiceType is spec.type on a Service — "ClusterIP", "NodePort",
	// "LoadBalancer" or "ExternalName".
	ServiceType string
	// ClusterIP is spec.clusterIP, which is the literal string "None" on a
	// headless Service and empty on an ExternalName.
	ClusterIP string
	// PodIP is status.podIP on a Pod, empty until it is scheduled and the CNI
	// has given it one.
	PodIP string
	// Host is the rule host on an Ingress.
	Host string
	// Port is the port to aim at: a Service port, a container port, or the
	// Ingress's own 80/443.
	Port int
	// PortName is the port's NAME, which is the only hint Kubernetes offers
	// about the protocol spoken on it — see SchemeForPort, which this reuses
	// rather than re-deciding.
	PortName string
	// Protocol is the port's protocol: "TCP", "UDP", "SCTP" or empty, which
	// Kubernetes defines as TCP.
	Protocol string
	// TLS reports whether an Ingress terminates TLS for Host.
	TLS bool
}

// ProbePlan is everything an adapter needs to perform one probe, and
// everything the UI needs to say what is about to happen before it does.
type ProbePlan struct {
	Vantage ProbeVantage
	Route   ProbeRoute
	// Kind, Namespace and Name identify what is being probed, carried through
	// so a result can name its subject without the caller holding it.
	Kind      string
	Namespace NamespaceName
	Name      string
	// Host is what gets resolved and dialled. It is a DNS name at the
	// in-cluster vantage — which is the point, since resolving it is half the
	// question — and an address or a name the API server resolves at the
	// local one.
	Host string
	Port int
	// Scheme is "http" or "https" when an HTTP request is worth making, and
	// empty when the probe is a bare TCP connect.
	Scheme string
	// Timeout is the ceiling on the whole probe. Always ProbeTimeout; carried
	// on the plan so the adapter never invents its own and the UI can say what
	// it is before the wait starts.
	Timeout time.Duration
}

// HTTP reports whether this plan includes an HTTP request.
func (p ProbePlan) HTTP() bool { return p.Scheme != "" }

// Address is host:port, for a message.
func (p ProbePlan) Address() string { return hostPort(p.Host, p.Port) }

func hostPort(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}

// PlanProbe decides what a probe of subject from vantage would do, or refuses
// with the reason.
//
// The refusals are the interesting half. Each one is a statement about the
// object or the vantage that stays true however many times it is pressed, so
// the panel renders it as an answer rather than retrying.
func PlanProbe(subject ProbeSubject, vantage ProbeVantage) (ProbePlan, error) {
	if subject.Port < 1 || subject.Port > 65535 {
		return ProbePlan{}, fmt.Errorf("%s/%s: %w", subject.Namespace, subject.Name, ErrProbeNoPort)
	}
	// Kubernetes defaults an unset protocol to TCP, so empty is TCP here for
	// the same reason forwardableProtocol treats it that way.
	if p := subject.Protocol; p != "" && !strings.EqualFold(p, "TCP") {
		return ProbePlan{}, fmt.Errorf("%s/%s port %d is %s: %w",
			subject.Namespace, subject.Name, subject.Port, p, ErrProbeNotTCP)
	}

	plan := ProbePlan{
		Vantage:   vantage,
		Kind:      subject.Kind,
		Namespace: subject.Namespace,
		Name:      subject.Name,
		Port:      subject.Port,
		Timeout:   ProbeTimeout,
	}

	switch subject.Kind {
	case "Service":
		return planService(plan, subject)
	case "Pod":
		return planPod(plan, subject)
	case "Ingress":
		return planIngress(plan, subject)
	default:
		return ProbePlan{}, fmt.Errorf("%s: %w", subject.Kind, ErrProbeVantageUnavailable)
	}
}

func planService(plan ProbePlan, subject ProbeSubject) (ProbePlan, error) {
	// The scheme is decided by the port's NAME at both vantages, through the
	// one function that already decides it for a port-forward's address. Two
	// implementations of "is this port https" would disagree eventually, and
	// an operator would have no way to tell which they were reading.
	plan.Scheme = SchemeForPort(subject.PortName)

	switch plan.Vantage {
	case VantageLocal:
		plan.Route = RouteServiceProxy
		// An ExternalName Service is a CNAME the cluster's DNS serves; it has
		// no cluster address and the API server's proxy has nothing to proxy
		// to. A headless Service has no single address either — that is what
		// headless MEANS — so proxying to it would pick an endpoint the
		// operator did not choose, or fail in a way that reads as the whole
		// Service being down.
		if strings.EqualFold(subject.ServiceType, "ExternalName") {
			return ProbePlan{}, fmt.Errorf("%s is an ExternalName service and has no cluster address: %w",
				subject.Name, ErrProbeNoAddress)
		}
		if subject.ClusterIP == "" || strings.EqualFold(subject.ClusterIP, "None") {
			return ProbePlan{}, fmt.Errorf("%s is headless and has no single address to reach: %w",
				subject.Name, ErrProbeNoAddress)
		}
		// The API server addresses the proxy by NAME, not by cluster IP: the
		// service proxy subresource is keyed on the object.
		plan.Host = subject.Name
		return plan, nil

	case VantageInCluster:
		plan.Route = RouteExec
		// The name a workload would actually use. Deliberately not the
		// fully-qualified ".svc.cluster.local": a cluster may run a different
		// cluster domain, and the short form is what the search path in every
		// pod's resolv.conf is FOR — so resolving it is evidence about the
		// resolver the workload really has.
		plan.Host = fmt.Sprintf("%s.%s.svc", subject.Name, subject.Namespace)
		return plan, nil
	}

	return ProbePlan{}, fmt.Errorf("%q: %w", plan.Vantage, ErrProbeVantageUnavailable)
}

func planPod(plan ProbePlan, subject ProbeSubject) (ProbePlan, error) {
	plan.Scheme = SchemeForPort(subject.PortName)

	switch plan.Vantage {
	case VantageLocal:
		plan.Route = RoutePortForward
		// The forward binds a local port; the host is this machine and the
		// port is not known until the forward is up, so the adapter fills it
		// in. What is planned here is which pod the tunnel goes to.
		plan.Host = "127.0.0.1"
		return plan, nil

	case VantageInCluster:
		plan.Route = RouteExec
		if subject.PodIP == "" {
			// Not an error and not a refusal of the feature: a pod that is
			// Pending has no address because nothing has scheduled it yet,
			// which is itself the answer to "why can nothing reach this".
			return ProbePlan{}, fmt.Errorf("%s has no pod IP yet: %w", subject.Name, ErrProbeNoAddress)
		}
		// The pod's own address rather than a name: a bare pod has no DNS
		// record unless a headless Service publishes one, so resolving a name
		// here would report a failure that says nothing about reachability.
		plan.Host = subject.PodIP
		return plan, nil
	}

	return ProbePlan{}, fmt.Errorf("%q: %w", plan.Vantage, ErrProbeVantageUnavailable)
}

func planIngress(plan ProbePlan, subject ProbeSubject) (ProbePlan, error) {
	if subject.Host == "" {
		return ProbePlan{}, fmt.Errorf("%s has no host in its rules: %w", subject.Name, ErrProbeNoAddress)
	}

	switch plan.Vantage {
	case VantageLocal:
		// REFUSED, and this is a product rule rather than a technical
		// limitation. Reaching an Ingress host from this machine means opening
		// a connection to a host that is not an API server, and the only
		// outbound destinations PodSteer has are the clusters your kubeconfig
		// names and, for the update check, GitHub. A browser is the tool for
		// the public address, and it is one click away.
		return ProbePlan{}, fmt.Errorf(
			"reaching %s would mean connecting to a host that is not an API server, which PodSteer does not do: %w",
			subject.Host, ErrProbeVantageUnavailable)

	case VantageInCluster:
		plan.Route = RouteExec
		plan.Host = subject.Host
		plan.Scheme = "http"
		if subject.TLS {
			plan.Scheme = "https"
		}
		return plan, nil
	}

	return ProbePlan{}, fmt.Errorf("%q: %w", plan.Vantage, ErrProbeVantageUnavailable)
}

// ProbeStepName names one thing a probe did, in the order it did them.
type ProbeStepName string

const (
	// StepDNS is turning a name into an address.
	StepDNS ProbeStepName = "dns"
	// StepConnect is the TCP handshake.
	StepConnect ProbeStepName = "connect"
	// StepHTTP is one request over the connection.
	StepHTTP ProbeStepName = "http"
)

// ProbeStatus is what became of one step.
type ProbeStatus string

const (
	// StatusOK means the step succeeded.
	StatusOK ProbeStatus = "ok"
	// StatusFailed means the step was performed and did not succeed. This is
	// an ordinary answer: it is what the operator pressed the button to find
	// out.
	StatusFailed ProbeStatus = "failed"
	// StatusSkipped means the step was deliberately not performed — the host
	// was already an address, so there was nothing to resolve; or an earlier
	// step failed and there was nothing left to try.
	StatusSkipped ProbeStatus = "skipped"
)

// ProbeStep is one step of a probe and what it produced.
//
// DNS AND CONNECT ARE SEPARATE STEPS AND ALWAYS WILL BE. Collapsing them is
// the single most common way this feature is got wrong elsewhere: a name that
// does not resolve and an address that refuses a connection need opposite next
// steps — one is a Service that does not exist or a resolver that is not
// serving it, the other is a policy, a listener that is not up, or a port
// nothing binds — and one red cross saying "unreachable" sends people to look
// at the wrong half of their cluster.
type ProbeStep struct {
	Name   ProbeStepName
	Status ProbeStatus
	// Detail is what happened, in the words of whatever performed it: the
	// address a name resolved to, the reason a connect failed, the HTTP status
	// line. Never paraphrased.
	Detail string
}

// ProbeObservation is what one vantage actually observed. Facts only: no
// verdict, because the verdict is NewProbeResult's job and belongs where a
// test can argue with it.
type ProbeObservation struct {
	Steps []ProbeStep
	// StatusCode is the HTTP status, or 0 when no request was made.
	StatusCode int
	// Elapsed is wall time for the whole probe.
	Elapsed time.Duration
}

// step returns the named step, and whether it is present.
func (o ProbeObservation) step(name ProbeStepName) (ProbeStep, bool) {
	for _, s := range o.Steps {
		if s.Name == name {
			return s, true
		}
	}
	return ProbeStep{}, false
}

// ProbeOutcome is the one-word verdict, and there are four of them because
// there are four genuinely different situations.
type ProbeOutcome string

const (
	// OutcomeReachable means the connection was established.
	OutcomeReachable ProbeOutcome = "reachable"
	// OutcomeNameNotResolved means the name did not resolve, so nothing was
	// ever dialled. Its own outcome, never folded into unreachable — see
	// ProbeStep.
	OutcomeNameNotResolved ProbeOutcome = "name_not_resolved"
	// OutcomeRefused means the address was dialled and the connection did not
	// establish.
	OutcomeRefused ProbeOutcome = "refused"
	// OutcomeUnknown means the probe did not get far enough to say — it was
	// cancelled, the container had nothing to probe with, or the output was
	// unreadable.
	OutcomeUnknown ProbeOutcome = "unknown"
)

// ProbeResult is a finished probe: what was tried, from where, and what
// happened.
type ProbeResult struct {
	Plan        ProbePlan
	Observation ProbeObservation
	Outcome     ProbeOutcome
	// Summary is one sentence naming the vantage, the target and the outcome.
	// Composed here rather than in the frontend because it is the one line
	// most people will read, and two implementations of it would eventually
	// say different things about the same probe.
	Summary string
}

// NewProbeResult shapes an observation into a verdict.
//
// The order is the order the steps happen in, and each earlier failure short-
// circuits the later ones — because a connect that was never attempted must
// not read as a connect that failed.
func NewProbeResult(plan ProbePlan, observation ProbeObservation) ProbeResult {
	result := ProbeResult{Plan: plan, Observation: observation, Outcome: OutcomeUnknown}

	if dns, ok := observation.step(StepDNS); ok && dns.Status == StatusFailed {
		result.Outcome = OutcomeNameNotResolved
		result.Summary = fmt.Sprintf("%s did not resolve %s.", vantageWords(plan), plan.Host)
		return result
	}

	connect, ok := observation.step(StepConnect)
	switch {
	case !ok:
		result.Summary = fmt.Sprintf("%s did not get as far as connecting to %s.", vantageWords(plan), plan.Address())
	case connect.Status == StatusFailed:
		result.Outcome = OutcomeRefused
		result.Summary = fmt.Sprintf("%s could not connect to %s.", vantageWords(plan), plan.Address())
	case connect.Status == StatusOK:
		result.Outcome = OutcomeReachable
		result.Summary = fmt.Sprintf("%s connected to %s%s.",
			vantageWords(plan), plan.Address(), httpWords(observation))
	default:
		result.Summary = fmt.Sprintf("%s did not attempt a connection to %s.", vantageWords(plan), plan.Address())
	}

	return result
}

// vantageWords says where a probe was performed from, in a form that fits in
// the middle of a sentence. It names the ROUTE as well as the vantage,
// because "from this machine" means two different things depending on whether
// the API server made the connection or merely carried it.
func vantageWords(plan ProbePlan) string {
	switch plan.Route {
	case RouteServiceProxy:
		return "The API server's service proxy"
	case RoutePortForward:
		return "This machine, through a port-forward,"
	case RouteExec:
		return "The container"
	default:
		return "The probe"
	}
}

func httpWords(observation ProbeObservation) string {
	if step, ok := observation.step(StepHTTP); ok && step.Status != StatusSkipped {
		if observation.StatusCode > 0 {
			return fmt.Sprintf(" and got HTTP %d", observation.StatusCode)
		}
		return " but the HTTP request did not complete"
	}
	return ""
}

// ProbeCommand is the command an in-cluster probe runs, and it lives beside
// ParseProbeOutput on purpose: the two are one protocol, and a test can assert
// that what this emits is what that reads. Splitting them across the domain
// and the adapter is how the two drift.
//
// ONE EXEC, ONE SHOT, AND NOTHING IS CREATED. No pod, no sidecar, no file
// written in somebody's container: a shell that asks the tools already in the
// image and prints a handful of lines. A container without any of those tools
// is the ordinary failure and says so by name, exactly as a container without
// tar does for a file copy.
//
// The host and port are interpolated only after PlanProbe has vetted them —
// a port is an integer in range, and a host is an address or a DNS name — and
// they are single-quoted regardless, so nothing an object's name can contain
// reaches the shell as syntax.
func ProbeCommand(plan ProbePlan) []string {
	seconds := int(plan.Timeout / time.Second)
	if seconds < 1 {
		seconds = 1
	}

	script := probeScript(plan.Host, plan.Port, plan.Scheme, seconds)
	return []string{"/bin/sh", "-c", script}
}

// probeScript writes the shell. Kept POSIX: `command -v`, `case`, no arrays,
// no bashisms, because the image on the far end is as likely to be busybox as
// it is to be Debian.
func probeScript(host string, port int, scheme string, seconds int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "H=%s; P=%s; T=%d\n", shellQuote(host), shellQuote(strconv.Itoa(port)), seconds)

	// Resolution first, and only when there is a name to resolve. An address
	// literal is reported as skipped rather than as a success, because
	// claiming DNS worked when nothing asked it is a small lie that makes the
	// panel useless for the case it exists to diagnose.
	b.WriteString(`case "$H" in
*[!0-9.]*)
  A=""
  if command -v getent >/dev/null 2>&1; then A=$(getent hosts "$H" 2>/dev/null | head -n 1 | cut -d' ' -f1); fi
  if [ -z "$A" ] && command -v nslookup >/dev/null 2>&1; then A=$(nslookup "$H" 2>/dev/null | awk '/^Address/{a=$NF} END{print a}'); fi
  if [ -n "$A" ]; then echo "dns ok resolved to $A"; else echo "dns failed the name did not resolve in this container"; exit 0; fi
  ;;
*)
  echo "dns skipped $H is already an address"
  ;;
esac
`)

	// The connect. nc is the only tool that answers the TCP question on its
	// own; where it is absent and the target speaks HTTP, the request itself
	// is the evidence and the connect step says so rather than claiming a
	// handshake nothing observed separately.
	b.WriteString(`if command -v nc >/dev/null 2>&1; then
  if nc -w "$T" -z "$H" "$P" >/dev/null 2>&1; then echo "connect ok tcp handshake completed"; else echo "connect failed nothing accepted a connection on that port"; fi
`)
	if scheme != "" {
		b.WriteString(`elif command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1; then
  echo "connect skipped no nc in this container; the HTTP request below is the evidence"
`)
	}
	b.WriteString(`else
  echo "unsupported this container has no nc, curl or wget to probe with"
  exit 0
fi
`)

	if scheme != "" {
		url := fmt.Sprintf("%s://%s:%d/", scheme, host, port)
		fmt.Fprintf(&b, `U=%s
if command -v curl >/dev/null 2>&1; then
  C=$(curl -k -s -o /dev/null -m "$T" -w '%%{http_code}' "$U" 2>/dev/null)
  if [ -n "$C" ] && [ "$C" != "000" ]; then echo "http ok $C"; else echo "http failed the request did not complete"; fi
elif command -v wget >/dev/null 2>&1; then
  C=$(wget --no-check-certificate -q -S -T "$T" -O /dev/null "$U" 2>&1 | awk '/HTTP\//{c=$2} END{print c}')
  if [ -n "$C" ]; then echo "http ok $C"; else echo "http failed the request did not complete"; fi
else
  echo "http skipped no curl or wget in this container"
fi
`, shellQuote(url))
	}

	return b.String()
}

// shellQuote wraps a value in single quotes, escaping any single quote it
// contains the only way sh allows.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// probeUnsupportedMarker is the line ProbeCommand's script prints when the
// container has nothing to probe with.
const probeUnsupportedMarker = "unsupported"

// ParseProbeOutput turns what an in-cluster probe wrote into an observation.
//
// Lines are "<step> <status> <detail...>". Anything else is ignored rather
// than refused, so a probe from a future build that learned a fourth step
// still yields the three this one understands — the same forgiveness the
// settings file's import gives an unknown field.
//
// The exception is the unsupported marker, which is returned as
// ErrProbeToolMissing's cause rather than as an observation: a container with
// nothing to probe with has not told us the target is unreachable, and
// rendering it as a failed probe would be a claim about somebody's Service
// based on somebody else's image.
func ParseProbeOutput(raw string, elapsed time.Duration) (ProbeObservation, bool, error) {
	observation := ProbeObservation{Elapsed: elapsed}
	recognised := false

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.SplitN(line, " ", 3)
		if fields[0] == probeUnsupportedMarker {
			detail := "this container has nothing to probe with"
			if len(fields) > 1 {
				detail = strings.TrimSpace(strings.Join(fields[1:], " "))
			}
			return ProbeObservation{Elapsed: elapsed}, false, errors.New(detail)
		}
		if len(fields) < 2 {
			continue
		}

		name, ok := probeStepName(fields[0])
		if !ok {
			continue
		}
		status, ok := probeStatus(fields[1])
		if !ok {
			continue
		}

		detail := ""
		if len(fields) == 3 {
			detail = strings.TrimSpace(fields[2])
		}

		// An HTTP step's detail begins with the status code, which is the one
		// number this whole answer is usually about, so it is lifted out
		// rather than left for the frontend to re-parse.
		if name == StepHTTP && status == StatusOK {
			code, rest := leadingCode(detail)
			if code > 0 {
				observation.StatusCode = code
				detail = rest
			}
		}

		observation.Steps = append(observation.Steps, ProbeStep{Name: name, Status: status, Detail: detail})
		recognised = true
	}

	if !recognised {
		return ProbeObservation{Elapsed: elapsed}, false, ErrProbeOutputUnreadable
	}
	return observation, true, nil
}

func probeStepName(field string) (ProbeStepName, bool) {
	switch ProbeStepName(field) {
	case StepDNS:
		return StepDNS, true
	case StepConnect:
		return StepConnect, true
	case StepHTTP:
		return StepHTTP, true
	default:
		return "", false
	}
}

func probeStatus(field string) (ProbeStatus, bool) {
	switch ProbeStatus(field) {
	case StatusOK:
		return StatusOK, true
	case StatusFailed:
		return StatusFailed, true
	case StatusSkipped:
		return StatusSkipped, true
	default:
		return "", false
	}
}

// leadingCode reads an HTTP status code off the front of a detail string,
// returning what is left. A detail whose first word is not a plausible status
// code leaves the code at zero and the detail untouched.
func leadingCode(detail string) (int, string) {
	head, rest, _ := strings.Cut(detail, " ")
	code, err := strconv.Atoi(head)
	if err != nil || code < 100 || code > 599 {
		return 0, detail
	}
	return code, strings.TrimSpace(rest)
}
