package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// writeVerbs are the words a tool that changes a cluster would be named with.
//
// A name check is a weak guard on its own — the strong one is the next test,
// which asserts the reading interfaces carry no write method at all — but it
// is the one that fails on a tool called "restart_workload" before anybody
// wires it to anything.
var writeVerbs = []string{
	"delete", "remove", "scale", "restart", "apply", "create", "update", "patch",
	"exec", "attach", "forward", "drain", "cordon", "evict", "rollback", "promote",
	"abort", "trigger", "set", "write", "reveal", "debug", "shell",
}

func TestEveryToolIsAReadAndNoneOfThemIsNamedForAWrite(t *testing.T) {
	server := newServer(t, newStub(t))

	if len(server.Tools()) == 0 {
		t.Fatal("no tools were built")
	}

	for _, tool := range server.Tools() {
		for _, verb := range writeVerbs {
			if strings.Contains(tool.Name, verb) {
				t.Errorf("tool %q is named for a write; this package offers reads only", tool.Name)
			}
		}
		if tool.Call == nil {
			t.Errorf("tool %q has no handler", tool.Name)
		}
	}
}

// The structural half of the same rule, and the one that cannot be worked
// around by naming a tool carefully: the interfaces this package accepts do
// not carry the writing methods, so no handler can reach one.
func TestTheReadingInterfacesCarryNoWriteAndNoSecretReveal(t *testing.T) {
	forbidden := map[string][]string{
		"ClusterReader": {"AddKubeconfig", "SetReadOnly", "PreviewKubeconfig"},
		"ResourceReader": {
			// The two calls that can return key material. Their absence IS
			// the redaction guarantee: an operator reveals one key in front
			// of the pane doing the asking, and an agent has no equivalent.
			"RevealSecretKey", "InspectTLSSecret",
		},
		"WorkloadReader": {"ScaleWorkload", "RestartRollout", "DeleteResource"},
		"LogReader":      {"DeleteResource", "ExecInPod", "UpdateResource", "SetSecretKey"},
	}

	interfaces := map[string]reflect.Type{
		"ClusterReader":  reflect.TypeOf((*ClusterReader)(nil)).Elem(),
		"ResourceReader": reflect.TypeOf((*ResourceReader)(nil)).Elem(),
		"WorkloadReader": reflect.TypeOf((*WorkloadReader)(nil)).Elem(),
		"LogReader":      reflect.TypeOf((*LogReader)(nil)).Elem(),
	}

	for name, iface := range interfaces {
		declared := make(map[string]bool, iface.NumMethod())
		for i := range iface.NumMethod() {
			declared[iface.Method(i).Name] = true
		}
		for _, method := range forbidden[name] {
			if declared[method] {
				t.Errorf("%s declares %s; narrowing these interfaces is what makes the tool set read-only", name, method)
			}
		}
	}
}

func TestASecretsManifestCarriesTheDecodedSizeAndNeverTheValue(t *testing.T) {
	stub := newStub(t)
	server := newServer(t, stub)

	result := call(t, server, "get_manifest", map[string]any{
		"cluster":   "staging",
		"kind":      "Secret",
		"namespace": "shop",
		"name":      "db",
	})
	if result.IsError {
		t.Fatalf("get_manifest failed: %s", resultText(t, result))
	}

	body := resultText(t, result)
	if strings.Contains(body, secretValue) {
		t.Fatal("the Secret's value crossed the boundary; base64 is an encoding, not a cipher")
	}
	if strings.Contains(body, "hunter2") {
		t.Fatal("the Secret's decoded value crossed the boundary")
	}
	if !strings.Contains(body, "hidden, 7 bytes") {
		t.Errorf("the placeholder must say something true about the value's shape:\n%s", body)
	}

	for _, revealed := range stub.revealRequested {
		if revealed {
			t.Fatal("a tool asked for the Secret to be revealed")
		}
	}
	if len(stub.revealRequested) == 0 {
		t.Fatal("no manifest was read at all, so this proves nothing")
	}
}

// Every tool, not only the manifest ones: a reveal added later to any handler
// fails here rather than in front of somebody's cluster.
func TestNoToolAnywhereAsksForASecretToBeRevealed(t *testing.T) {
	stub := newStub(t)
	server := newServer(t, stub)

	for _, tool := range server.Tools() {
		// Every argument each tool could want, so schema validation passes
		// whichever subset this tool declares. Unknown arguments are refused,
		// so only the declared ones are sent.
		possible := map[string]any{
			"cluster": "staging", "namespace": "shop", "kind": "Secret", "name": "db",
			"pod": "web-1", "container": "web", "verb": "get", "resource": "pods",
			"scope": "namespace", "node": "", "warnings_only": false, "previous": false,
		}
		arguments := map[string]any{}
		for key := range tool.Schema.Properties {
			if value, offered := possible[key]; offered && value != "" {
				arguments[key] = value
			}
		}
		// list_pods refuses a namespace and a node together, and the node is
		// the optional one.
		delete(arguments, "node")

		if _, err := tool.Call(context.Background(), Arguments(arguments)); err != nil {
			// A tool that cannot answer this fixture is fine; what matters is
			// what it asked the cluster for on the way.
			continue
		}
	}

	for _, revealed := range stub.revealRequested {
		if revealed {
			t.Fatal("a tool asked for a Secret to be revealed")
		}
	}
}

// The rule this whole adapter turns on: an agent handed an empty list reports
// that the cluster holds no such objects, with complete confidence.
func TestARefusalComesBackAsARefusalRatherThanAnEmptyResult(t *testing.T) {
	stub := newStub(t)
	stub.failWith = ports.ErrForbidden
	// Connections is what the cluster helper checks first, so the cluster has
	// to look open for the refusal to come from the READ rather than from the
	// connect attempt.
	stub.connected = stub.clusters
	server := newServer(t, stub)

	result := call(t, server, "list_pods", map[string]any{"cluster": "staging", "namespace": "shop"})

	if !result.IsError {
		t.Fatal("a forbidden read reported success")
	}

	var failed failure
	if err := json.Unmarshal([]byte(resultText(t, result)), &failed); err != nil {
		t.Fatalf("a failure must still be JSON the model can read: %v", err)
	}
	if failed.Error != codeForbidden {
		t.Errorf("classified as %q, want %q", failed.Error, codeForbidden)
	}
	if !strings.Contains(failed.Message, "not an empty result") {
		t.Errorf("the message must say this is a refusal: %q", failed.Message)
	}
	if strings.Contains(resultText(t, result), `"items"`) {
		t.Error("a refusal must not be shaped like a list with nothing in it")
	}
}

func TestEachKindOfFailureKeepsItsOwnClassification(t *testing.T) {
	cases := map[string]struct {
		err  error
		want failureCode
	}{
		"forbidden":         {ports.ErrForbidden, codeForbidden},
		"unreachable":       {ports.ErrUnreachable, codeUnreachable},
		"unauthenticated":   {ports.ErrUnauthenticated, codeUnauthenticated},
		"not found":         {ports.ErrNotFound, codeNotFound},
		"credential plugin": {ports.ErrCredentialPluginMissing, codeCredentialPlugin},
		"no metrics API":    {ports.ErrMetricsUnavailable, codeMetricsUnavailable},
		"unknown context":   {domain.ErrClusterNotFound, codeClusterNotFound},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newStub(t)
			stub.failWith = testCase.err
			stub.connected = stub.clusters
			server := newServer(t, stub)

			result := call(t, server, "assess_cluster", map[string]any{"cluster": "staging"})
			if !result.IsError {
				t.Fatal("the failure was reported as success")
			}

			var failed failure
			if err := json.Unmarshal([]byte(resultText(t, result)), &failed); err != nil {
				t.Fatalf("decoding failure: %v", err)
			}
			if failed.Error != testCase.want {
				t.Errorf("classified as %q, want %q", failed.Error, testCase.want)
			}
		})
	}
}

// Two scopes at once: answering one of them silently ignores half of what was
// asked for, and the caller cannot tell which half.
func TestListPodsRefusesANamespaceAndANodeTogether(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "tools/call", map[string]any{
		"name":      "list_pods",
		"arguments": map[string]any{"cluster": "staging", "namespace": "shop", "node": "node-a"},
	})
	if result.Error == nil {
		t.Fatal("both scopes were accepted")
	}
	if result.Error.Code != rpcInvalidParams {
		t.Errorf("code %d, want %d", result.Error.Code, rpcInvalidParams)
	}
}

func TestAKindTheClusterDoesNotServeIsRefusedByName(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "tools/call", map[string]any{
		"name":      "list_resources",
		"arguments": map[string]any{"cluster": "staging", "kind": "Widget"},
	})
	if result.Error == nil {
		t.Fatal("an uninstalled kind was accepted")
	}
	if !strings.Contains(result.Error.Message, "Widget") || !strings.Contains(result.Error.Message, "list_kinds") {
		t.Errorf("the refusal must name the kind and where to look: %q", result.Error.Message)
	}
}

// "Application" exists in three API groups. Picking one would answer a
// question about a different object entirely.
func TestAnAmbiguousKindNamesTheCandidatesRatherThanChoosing(t *testing.T) {
	stub := newStub(t)
	stub.kinds = append(stub.kinds,
		domain.ResourceKind{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications", Kind: "Application", Namespaced: true},
		domain.ResourceKind{Group: "app.k8s.io", Version: "v1beta1", Resource: "applications", Kind: "Application", Namespaced: true},
	)
	server := newServer(t, stub)

	result := answer(t, server, 1, "tools/call", map[string]any{
		"name":      "list_resources",
		"arguments": map[string]any{"cluster": "staging", "kind": "Application"},
	})
	if result.Error == nil {
		t.Fatal("an ambiguous kind was resolved rather than refused")
	}
	for _, id := range []string{"argoproj.io/v1alpha1/applications", "app.k8s.io/v1beta1/applications"} {
		if !strings.Contains(result.Error.Message, id) {
			t.Errorf("the refusal must offer %q: %q", id, result.Error.Message)
		}
	}
}

// The catalogue id is what list_kinds reports, and it must always resolve —
// the friendly forms are a convenience on top of it, never instead of it.
func TestAKindResolvesByCatalogueIdAndByName(t *testing.T) {
	server := newServer(t, newStub(t))

	for _, named := range []string{"apps/v1/deployments", "Deployment", "deployments", "deployment"} {
		resolved := call(t, server, "get_manifest", map[string]any{
			"cluster": "staging", "kind": named, "namespace": "shop", "name": "web",
		})
		if resolved.IsError {
			t.Errorf("%q did not resolve: %s", named, resultText(t, resolved))
		}
	}
}

func TestAClusterScopedKindRefusesANamespaceRatherThanIgnoringIt(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "tools/call", map[string]any{
		"name":      "get_manifest",
		"arguments": map[string]any{"cluster": "staging", "kind": "Node", "namespace": "shop", "name": "node-a"},
	})
	if result.Error == nil {
		t.Fatal("a namespace on a cluster-scoped kind was accepted")
	}
}

func TestALogReadNeverFollowsAndIsBounded(t *testing.T) {
	stub := newStub(t)
	server := newServer(t, stub)

	result := call(t, server, "get_logs", map[string]any{
		"cluster": "staging", "namespace": "shop", "pod": "web-1", "tail_lines": float64(1),
	})
	if result.IsError {
		t.Fatalf("get_logs failed: %s", resultText(t, result))
	}

	if len(stub.logOptions) != 1 {
		t.Fatalf("the log was opened %d times", len(stub.logOptions))
	}
	opts := stub.logOptions[0]
	if opts.Follow {
		t.Error("a followed stream never returns, and a tool call that never returns is an agent that has stopped")
	}
	if opts.TailLines != 1 {
		t.Errorf("tail_lines was sent as %d, want 1", opts.TailLines)
	}
	if opts.LimitBytes != logLimitBytes {
		t.Errorf("LimitBytes was %d, want the cap %d", opts.LimitBytes, logLimitBytes)
	}

	var logs logsOut
	if err := json.Unmarshal([]byte(resultText(t, result)), &logs); err != nil {
		t.Fatalf("decoding logs: %v", err)
	}
	if len(logs.Lines) != 1 {
		t.Errorf("returned %d lines, want the 1 asked for", len(logs.Lines))
	}
	if !logs.Truncated {
		t.Error("the stream carried more than was returned and must say so, or an absent line reads as evidence")
	}
}

// Truncation is never silent: a model told "42 pods" when there were 400
// reasons about the 42 with complete confidence.
func TestATruncatedListStatesTheTrueTotal(t *testing.T) {
	stub := newStub(t)
	extra := stub.pods[0]
	stub.pods = []domain.Pod{extra, extra, extra}
	server := newServer(t, stub)

	result := call(t, server, "list_pods", map[string]any{
		"cluster": "staging", "namespace": "shop", "limit": float64(1),
	})

	var listed rows[podRow]
	if err := json.Unmarshal([]byte(resultText(t, result)), &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(listed.Items) != 1 {
		t.Errorf("returned %d rows, want the 1 asked for", len(listed.Items))
	}
	if listed.Total != 3 {
		t.Errorf("reported a total of %d, want the true 3", listed.Total)
	}
	if !listed.Truncated {
		t.Error("a truncated list must say so")
	}
}

func TestAMissingPodIsReportedRatherThanAssessedAsHealthy(t *testing.T) {
	server := newServer(t, newStub(t))

	result := call(t, server, "assess_pod", map[string]any{
		"cluster": "staging", "namespace": "shop", "pod": "not-here",
	})
	if !result.IsError {
		t.Fatal("an absent pod produced an assessment; no findings reads as a healthy pod")
	}

	var failed failure
	if err := json.Unmarshal([]byte(resultText(t, result)), &failed); err != nil {
		t.Fatalf("decoding failure: %v", err)
	}
	if failed.Error != codeNotFound {
		t.Errorf("classified as %q, want %q", failed.Error, codeNotFound)
	}
}

func TestAPodAssessmentCarriesTheDomainsFindingsAndContainers(t *testing.T) {
	server := newServer(t, newStub(t))

	result := call(t, server, "assess_pod", map[string]any{
		"cluster": "staging", "namespace": "shop", "pod": "web-1",
	})
	if result.IsError {
		t.Fatalf("assess_pod failed: %s", resultText(t, result))
	}

	var assessment podAssessmentOut
	if err := json.Unmarshal([]byte(resultText(t, result)), &assessment); err != nil {
		t.Fatalf("decoding assessment: %v", err)
	}
	if assessment.Pod.Name != "web-1" {
		t.Errorf("assessed %q", assessment.Pod.Name)
	}
	if len(assessment.Containers) != 1 || assessment.Containers[0].Name != "web" {
		t.Errorf("containers were %+v", assessment.Containers)
	}
	// The domain's own verdict, carried across whole. WHICH findings a pod
	// raises is argued over in pod_assessment_test.go; what matters here is
	// that they arrive with the advice on them, since the advice is the half
	// a raw field dump never carries and the reason this tool exists at all.
	if len(assessment.Findings) == 0 {
		t.Fatal("the fixture pod is a bare pod running a moving tag and must produce findings")
	}
	for _, finding := range assessment.Findings {
		if finding.Title == "" || finding.Advice == "" || finding.Severity == "" {
			t.Errorf("finding lost part of the domain's verdict: %+v", finding)
		}
	}
}

// A cluster already open is not reconnected: Connect re-runs discovery, and
// paying for that on every tool call would make each answer slower than the
// one before it for no gain.
func TestAnAlreadyConnectedClusterIsNotConnectedAgain(t *testing.T) {
	stub := newStub(t)
	server := newServer(t, stub)

	call(t, server, "list_namespaces", map[string]any{"cluster": "staging"})
	call(t, server, "list_namespaces", map[string]any{"cluster": "staging"})
	call(t, server, "list_kinds", map[string]any{"cluster": "staging"})

	if len(stub.connects) != 1 {
		t.Errorf("connected %d times, want once", len(stub.connects))
	}
}

func TestDescribeReturnsAManifestEvenWhenTheEventsAreRefused(t *testing.T) {
	stub := newStub(t)
	stub.eventsFail = ports.ErrForbidden
	server := newServer(t, stub)

	result := call(t, server, "describe_resource", map[string]any{
		"cluster": "staging", "kind": "Secret", "namespace": "shop", "name": "db",
	})
	if result.IsError {
		t.Fatalf("an account that may read an object and not its events got nothing: %s", resultText(t, result))
	}
	if !contains(t, result, "eventsUnreadable") {
		t.Error("the refusal of the events half must be named rather than read as no events")
	}
	if contains(t, result, secretValue) {
		t.Fatal("describe leaked the Secret's value")
	}
}

func TestNewRefusesAServerMissingAnyReader(t *testing.T) {
	stub := newStub(t)

	if _, err := New(Deps{Kinds: stub, Workloads: stub, Events: stub, Resources: stub, Overview: stub, RBAC: stub, Logs: stub}); err == nil {
		t.Error("a server with no ClusterReader was built")
	}
	if _, err := New(Deps{Clusters: stub, Kinds: stub, Workloads: stub, Events: stub, Resources: stub, Overview: stub, RBAC: stub}); err == nil {
		t.Error("a server with no LogReader was built")
	}
}
