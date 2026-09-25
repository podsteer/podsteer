package wails

import (
	"errors"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Wire types and bindings for the two on-request inspections. As everywhere
// else in this package these structs ARE the frontend contract — Wails
// generates TypeScript from them — so a field rename is a breaking change.

// ProbeStep is one step of a probe and what it produced.
//
// DNS and connect are separate steps on the wire as well as in the domain,
// because the panel has to be able to say which of them failed. A single
// boolean would have been smaller and would have thrown away the only thing
// that decides where an operator looks next.
type ProbeStep struct {
	// Name is "dns", "connect" or "http".
	Name string `json:"name"`
	// Status is "ok", "failed" or "skipped". Skipped is not a failure: an
	// address literal has nothing to resolve.
	Status string `json:"status"`
	// Detail is what happened, in the words of whatever performed it.
	Detail string `json:"detail"`
}

// ProbeResult is a finished reachability probe.
type ProbeResult struct {
	// Vantage is "local" or "in_cluster" — WHERE the answer is true of, and
	// the panel is expected to say it.
	Vantage string `json:"vantage"`
	// Route is "service_proxy", "port_forward" or "exec": how the vantage
	// reached the target, which decides what a success is evidence of.
	Route string `json:"route"`
	// Target is host:port as it was actually addressed.
	Target string `json:"target"`
	// Scheme is "http", "https" or "" when the probe was a bare TCP connect.
	Scheme string `json:"scheme"`
	// Outcome is "reachable", "name_not_resolved", "refused" or "unknown".
	Outcome string `json:"outcome"`
	// Summary is one sentence naming the vantage, the target and the outcome.
	Summary string `json:"summary"`
	// Steps are what the probe did, in order.
	Steps []ProbeStep `json:"steps"`
	// StatusCode is the HTTP status, or 0 when no request was made.
	StatusCode int `json:"statusCode"`
	// ElapsedMs is wall time for the whole probe.
	ElapsedMs int64 `json:"elapsedMs"`
	// TimeoutMs is the ceiling that applied, so the panel can say what it is
	// rather than implying the wait was unbounded.
	TimeoutMs int64 `json:"timeoutMs"`
}

// ImageReport is what Kubernetes reports about one container's image.
type ImageReport struct {
	Container string `json:"container"`
	// Declared is spec.containers[].image; Resolved is what the kubelet says
	// it is running. Drift reports that they differ.
	Declared string `json:"declared"`
	Resolved string `json:"resolved"`
	Drift    bool   `json:"drift"`
	// Registry, Repository and Tag are the resolved reference parsed, with a
	// runtime's own defaults applied. Empty when it could not be parsed,
	// which ReferenceReadable reports.
	Registry          string `json:"registry"`
	Repository        string `json:"repository"`
	Tag               string `json:"tag"`
	ReferenceReadable bool   `json:"referenceReadable"`
	// Digest is the content digest Kubernetes reported for what is running.
	Digest string `json:"digest"`
	// DigestNote explains a node and a container status that disagree about
	// it, which is what a multi-platform image looks like.
	DigestNote string `json:"digestNote"`
	// SizeBytes and SizeStatus are the image's size on the node that pulled
	// it. READ SizeStatus FIRST: "measured", "not_reported" or "unreadable",
	// and SizeBytes is zero for the last two — a dash, never a nought.
	SizeBytes  int64  `json:"sizeBytes"`
	SizeStatus string `json:"sizeStatus"`
	// SizeSource names whose number it is, or why there is none.
	SizeSource string `json:"sizeSource"`
	// OtherNames are the other references the node knows the same image by.
	OtherNames []string `json:"otherNames"`
	// PullPolicy and PullSecrets quote the pod's spec. The Secrets are NAMES
	// only and nothing on this path reads their contents.
	PullPolicy  string   `json:"pullPolicy"`
	PullSecrets []string `json:"pullSecrets"`
	// Credentialed reports that the pull used a Secret, so the panel can say
	// what it did not look at.
	Credentialed bool `json:"credentialed"`
	// CredentialNote is the sentence for that case.
	CredentialNote string `json:"credentialNote"`
	// Bounded is the line saying what is NOT here and why — the layers, the
	// entrypoint, the labels. Always set, and the panel always shows it:
	// empty space where layers would be is a claim nothing checked.
	Bounded string `json:"bounded"`
}

// InspectAPI exposes the two on-request inspections.
//
// NOTHING HERE IS ON THE REFRESH TICK, and that is the rule the whole surface
// exists to make visible — see ports.InspectService. A probe is one act the
// operator asked for; the panel above it shows the answer with the time it
// was taken and waits to be asked again.
type InspectAPI struct {
	inspect ports.InspectService
	app     *App
	// logger receives the operation and the error, never a probe's output —
	// see InspectService's audit line, which is the one record of an exec.
	logger *slog.Logger
}

// NewInspectAPI returns the bound inspection API.
func NewInspectAPI(inspect ports.InspectService, app *App, logger *slog.Logger) (*InspectAPI, error) {
	switch {
	case inspect == nil:
		return nil, errors.New("wails: InspectAPI requires an InspectService")
	case app == nil:
		return nil, errors.New("wails: InspectAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &InspectAPI{
		inspect: inspect,
		app:     app,
		logger:  logger.With(slog.String("api", "inspect")),
	}, nil
}

// ProbeSubjectInput is what the frontend read off the object it is offering
// to probe. Every field is a quotation of the manifest already on screen, so
// planning a probe costs no read at all.
type ProbeSubjectInput struct {
	// Kind is the Kubernetes Kind verbatim: "Service", "Pod" or "Ingress".
	// Verbatim matters for the same reason a followable graph node's does —
	// a lowercased plural matches nothing and the probe silently refuses.
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	ServiceType string `json:"serviceType"`
	ClusterIP   string `json:"clusterIp"`
	PodIP       string `json:"podIp"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	PortName    string `json:"portName"`
	Protocol    string `json:"protocol"`
	TLS         bool   `json:"tls"`
}

// ProbeFromHere probes a target from this machine.
//
// One act, one answer, never a tick. It reaches the cluster the only way this
// process reaches anything — through the API server named in the kubeconfig —
// and refuses outright for anything that would mean contacting a host that is
// not one, which is why an Ingress offers only the in-cluster vantage.
func (i *InspectAPI) ProbeFromHere(clusterID string, subject ProbeSubjectInput) (ProbeResult, error) {
	ctx, cancel := i.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromHere", err)
	}

	parsed, err := toProbeSubject(subject)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromHere", err)
	}

	result, err := i.inspect.ProbeFromHere(ctx, id, parsed)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromHere", err)
	}

	return toProbeResult(result), nil
}

// ProbeFromPod probes a target from inside a container the operator chose.
//
// A WRITE-SHAPED ACT: it runs a command in somebody's container, so it is
// refused on a cluster marked read-only and leaves one audit line naming the
// cluster, namespace, pod, container and target. What it never records is
// what came back.
func (i *InspectAPI) ProbeFromPod(clusterID, namespace, podName, containerName string, subject ProbeSubjectInput) (ProbeResult, error) {
	ctx, cancel := i.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromPod", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromPod", err)
	}

	if podName == "" || containerName == "" {
		return ProbeResult{}, apiError(i.logger, "ProbeFromPod", errProbeNoContainer)
	}

	parsed, err := toProbeSubject(subject)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromPod", err)
	}

	result, err := i.inspect.ProbeFromPod(ctx, id, ns, podName, containerName, parsed)
	if err != nil {
		return ProbeResult{}, apiError(i.logger, "ProbeFromPod", err)
	}

	return toProbeResult(result), nil
}

// ImageReport describes one container's image from what Kubernetes reports.
func (i *InspectAPI) ImageReport(clusterID, namespace, podName, containerName string) (ImageReport, error) {
	ctx, cancel := i.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ImageReport{}, apiError(i.logger, "ImageReport", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return ImageReport{}, apiError(i.logger, "ImageReport", err)
	}

	if podName == "" || containerName == "" {
		return ImageReport{}, apiError(i.logger, "ImageReport", errProbeNoContainer)
	}

	report, err := i.inspect.ImageReport(ctx, id, ns, podName, containerName)
	if err != nil {
		return ImageReport{}, apiError(i.logger, "ImageReport", err)
	}

	return toImageReport(report), nil
}

// toProbeSubject validates the namespace and carries the rest across.
// Everything else the frontend sent is vetted by domain.PlanProbe, which is
// where the rules about ports, protocols and addresses live.
func toProbeSubject(subject ProbeSubjectInput) (domain.ProbeSubject, error) {
	ns, err := domain.NewNamespaceName(subject.Namespace)
	if err != nil {
		return domain.ProbeSubject{}, err
	}

	return domain.ProbeSubject{
		Kind:        subject.Kind,
		Namespace:   ns,
		Name:        subject.Name,
		ServiceType: subject.ServiceType,
		ClusterIP:   subject.ClusterIP,
		PodIP:       subject.PodIP,
		Host:        subject.Host,
		Port:        subject.Port,
		PortName:    subject.PortName,
		Protocol:    subject.Protocol,
		TLS:         subject.TLS,
	}, nil
}

func toProbeResult(result domain.ProbeResult) ProbeResult {
	steps := make([]ProbeStep, 0, len(result.Observation.Steps))
	for _, step := range result.Observation.Steps {
		steps = append(steps, ProbeStep{
			Name:   string(step.Name),
			Status: string(step.Status),
			Detail: step.Detail,
		})
	}

	return ProbeResult{
		Vantage:    string(result.Plan.Vantage),
		Route:      string(result.Plan.Route),
		Target:     result.Plan.Address(),
		Scheme:     result.Plan.Scheme,
		Outcome:    string(result.Outcome),
		Summary:    result.Summary,
		Steps:      steps,
		StatusCode: result.Observation.StatusCode,
		ElapsedMs:  result.Observation.Elapsed.Milliseconds(),
		TimeoutMs:  result.Plan.Timeout.Milliseconds(),
	}
}

func toImageReport(report domain.ImageReport) ImageReport {
	out := ImageReport{
		Container:         report.Container,
		Declared:          report.Declared,
		Resolved:          report.Resolved,
		Drift:             report.Drift,
		Registry:          report.Reference.Registry,
		Repository:        report.Reference.Repository,
		Tag:               report.Reference.Tag,
		ReferenceReadable: report.ReferenceReadable,
		Digest:            report.Digest,
		DigestNote:        report.DigestNote,
		SizeBytes:         report.SizeBytes,
		SizeStatus:        string(report.SizeStatus),
		SizeSource:        report.SizeSource,
		OtherNames:        report.OtherNames,
		PullPolicy:        report.PullPolicy,
		PullSecrets:       report.PullSecrets,
		Credentialed:      report.Credentialed,
		Bounded:           report.Bounded,
	}

	if report.Credentialed {
		out.CredentialNote = domain.ImageCredentialNote
	}

	// Null and empty are the same thing to the panel, and a null array is one
	// more branch every consumer has to remember — the same normalisation
	// FileCopyDoneEvent makes for its notes.
	if out.OtherNames == nil {
		out.OtherNames = []string{}
	}
	if out.PullSecrets == nil {
		out.PullSecrets = []string{}
	}

	return out
}
