package k8s

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

func expressionProjection(pairs ...string) domain.Projection {
	expressions := make([]domain.CustomExpression, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		expressions = append(expressions, domain.CustomExpression{ID: pairs[i], Path: pairs[i+1]})
	}
	return domain.Projection{}.WithExpressions(expressions)
}

func expressionPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "web", Labels: map[string]string{"app": "api"}},
		Spec: corev1.PodSpec{
			NodeName:     "node-1",
			Containers:   []corev1.Container{{Name: "app", Image: "nginx:1.27"}},
			Tolerations:  []corev1.Toleration{{Key: "workload", Value: "batch"}},
			NodeSelector: map[string]string{"disk": "ssd"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.1.2.3"},
	}
}

// THE SAME EXPRESSION MUST MEAN THE SAME THING ON BOTH LIST PATHS. A typed
// list walks a Go struct; a server-printed table carries the object as
// generic JSON. If those two disagreed, a column would answer differently
// depending on whether the kind happens to have a purpose-built list — which
// is exactly the class of bug the whole feature is arranged to avoid.
func TestCustomColumnsReadTheSameFromAStructAndFromGenericData(t *testing.T) {
	t.Parallel()

	projection := expressionProjection(
		"c1", ".status.phase",
		"c2", ".spec.nodeName",
		"c3", ".spec.containers[0].image",
	)

	fromStruct := customColumns(projection, expressionPod())

	generic := map[string]any{
		"status": map[string]any{"phase": "Running"},
		"spec": map[string]any{
			"nodeName":   "node-1",
			"containers": []any{map[string]any{"image": "nginx:1.27"}},
		},
	}
	fromGeneric := customColumns(projection, generic)

	for id, want := range map[string]string{"c1": "Running", "c2": "node-1", "c3": "nginx:1.27"} {
		if fromStruct[id] != want {
			t.Errorf("struct %s = %q, want %q", id, fromStruct[id], want)
		}
		if fromGeneric[id] != want {
			t.Errorf("generic %s = %q, want %q", id, fromGeneric[id], want)
		}
	}
}

func TestCustomColumnsAcceptBracedAndUnbracedPaths(t *testing.T) {
	t.Parallel()

	// kubectl accepts both and people paste both, so a column that took only
	// one of them would refuse an expression that works in their shell.
	values := customColumns(expressionProjection("a", ".status.phase", "b", "{.status.phase}"), expressionPod())

	if values["a"] != "Running" || values["b"] != "Running" {
		t.Fatalf("values = %v, want both forms to read Running", values)
	}
}

func TestCustomColumnsRenderAMissingFieldAsEmpty(t *testing.T) {
	t.Parallel()

	// The same answer an absent label gives. A pod with no such field is not
	// an error, and a cell saying so in words would be noise on every row.
	values := customColumns(expressionProjection("a", ".status.nothingHere", "b", ".spec.containers[9].image"), expressionPod())

	if values["a"] != "" || values["b"] != "" {
		t.Fatalf("values = %v, want empty cells for what is not there", values)
	}
}

func TestCustomColumnsSayWhenThePathIsTheProblem(t *testing.T) {
	t.Parallel()

	// A path that cannot parse is the operator's mistake, not the cluster's,
	// and it is worth distinguishing from a field that is merely absent —
	// otherwise a typo reads as "this object does not have that", which sends
	// somebody to look at the wrong thing entirely.
	values := customColumns(expressionProjection("a", ".spec.containers[", "b", "{.status"), expressionPod())

	for id, value := range values {
		if !strings.HasPrefix(value, "!") {
			t.Errorf("%s = %q, want a cell that says the path is wrong", id, value)
		}
	}
}

func TestCustomColumnsReadWhatTheWatchStoreWouldHaveStripped(t *testing.T) {
	t.Parallel()

	// The reason a list with expressions bypasses the watch: stripPod removes
	// tolerations and nodeSelector before storing, so these two would read
	// blank from the store and full from the network. Here they are read from
	// a whole object, which is what that bypass guarantees the evaluator gets.
	values := customColumns(expressionProjection(
		"a", ".spec.tolerations[0].key",
		"b", ".spec.nodeSelector.disk",
	), expressionPod())

	if values["a"] != "workload" || values["b"] != "ssd" {
		t.Fatalf("values = %v, want the fields the store would have dropped", values)
	}

	// And the assertion that keeps this honest: stripPod really does remove
	// them, so the bypass is load-bearing rather than defensive.
	stripped, err := stripPod(expressionPod())
	if err != nil {
		t.Fatalf("stripPod: %v", err)
	}
	if pod, ok := stripped.(*corev1.Pod); !ok || len(pod.Spec.Tolerations) != 0 || pod.Spec.NodeSelector != nil {
		t.Fatal("stripPod no longer drops these fields; the watch bypass may no longer be needed")
	}
}

func TestCustomColumnsAreNilWhenNothingWasAsked(t *testing.T) {
	t.Parallel()

	// Nil rather than an empty map, so an object read from the store and the
	// same object read from the network compare equal — the rule
	// Projection.Annotations already follows.
	if values := customColumns(domain.Projection{}, expressionPod()); values != nil {
		t.Fatalf("values = %v, want nil", values)
	}
}
