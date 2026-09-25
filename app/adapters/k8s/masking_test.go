package k8s

import (
	"strings"
	"testing"

	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func secretObject(data map[string]any) *unstructuredv1.Unstructured {
	return &unstructuredv1.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "creds", "namespace": "default"},
		"data":       data,
	}}
}

func TestMaskSecretDataRemovesTheValueNotJustTheEncoding(t *testing.T) {
	t.Parallel()

	// "password" and "s3cr3t-token" base64-encoded. BASE64 IS NOT MASKING:
	// leaving these in place, as the incumbents do, means a screenshot of the
	// pane has leaked the credential to anyone who can type `base64 -d`.
	object := secretObject(map[string]any{
		"pw":    "cGFzc3dvcmQ=",
		"token": "czNjcjN0LXRva2Vu",
	})

	maskSecretData(object)

	values, found, err := unstructuredv1.NestedMap(object.Object, "data")
	if err != nil || !found {
		t.Fatalf("data went missing entirely: found=%v err=%v", found, err)
	}

	for key, value := range values {
		got, _ := value.(string)
		if strings.Contains(got, "cGFzc3dvcmQ") || strings.Contains(got, "czNjcjN0") {
			t.Errorf("%s = %q, still carries the encoded value", key, got)
		}
		if !strings.HasPrefix(got, "<hidden,") {
			t.Errorf("%s = %q, want a placeholder", key, got)
		}
	}

	// The DECODED size, so the placeholder says something true about the
	// value — "password" is eight bytes, not the twelve its base64 takes.
	if got := values["pw"]; got != "<hidden, 8 bytes>" {
		t.Errorf("pw = %q, want the decoded length", got)
	}
}

func TestMaskSecretDataLeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()

	// A ConfigMap holding something that looks sensitive. Guessing at which
	// fields of an arbitrary kind are secret would mask things arbitrarily
	// and still miss the ones that matter, so nothing but a Secret is touched.
	configMap := &unstructuredv1.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"data":       map[string]any{"password": "hunter2"},
	}}

	maskSecretData(configMap)

	values, _, _ := unstructuredv1.NestedMap(configMap.Object, "data")
	if values["password"] != "hunter2" {
		t.Errorf("password = %q, want it untouched: this is not a Secret", values["password"])
	}
}

func TestMaskSecretDataHidesAValueThatIsNotAString(t *testing.T) {
	t.Parallel()

	// A NON-STRING SCALAR USED TO BE SKIPPED ENTIRELY, which was a hole in
	// this function long before the Helm pane started sharing it.
	//
	// The API server rejects a Secret whose value is not a string, so this
	// cannot arrive on a live object. It arrives readily on a MANIFEST that
	// was never accepted: Helm writes its release Secret before applying
	// what it rendered, so a `failed` revision's stored manifest routinely
	// holds exactly the object the API server refused — and an unquoted
	// `stringData: {pin: 483920}` parses as a number, which the old code
	// left sitting in the clear.
	object := &unstructuredv1.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "creds"},
		"stringData": map[string]any{
			"pin":     float64(483920),
			"enabled": true,
			"missing": nil,
			"normal":  "hunter2",
		},
	}}

	maskSecretData(object)

	values, found, err := unstructuredv1.NestedMap(object.Object, "stringData")
	if err != nil || !found {
		t.Fatalf("stringData went missing entirely: found=%v err=%v", found, err)
	}

	for _, key := range []string{"pin", "enabled", "missing"} {
		got, isString := values[key].(string)
		if !isString || !strings.HasPrefix(got, "<hidden,") {
			t.Errorf("%s = %#v, want it hidden — a value that is not a string is still a value", key, values[key])
		}
	}
	if strings.Contains(strings.Join([]string{
		toStringForTest(values["pin"]),
		toStringForTest(values["enabled"]),
	}, " "), "483920") {
		t.Error("the number reached the output in the clear")
	}
	if got, _ := values["normal"].(string); got != "<hidden, 7 bytes>" {
		t.Errorf("normal = %q, want the ordinary stringData placeholder", got)
	}
}

func TestMaskSecretDataLeavesANestedStructureAlone(t *testing.T) {
	t.Parallel()

	// THE ONE SHAPE NOT MASKED, and deliberately. A map or a list is not a
	// Secret value at all — no encoding of Secret data has that shape — so
	// replacing it would be rewriting a structure rather than hiding a
	// value, and the placeholder would claim something about bytes nothing
	// measured. It stays visible for the reason a ConfigMap does.
	object := secretObject(map[string]any{
		"nested": map[string]any{"inner": "value"},
		"listy":  []any{"one", "two"},
		"pw":     "cGFzc3dvcmQ=",
	})

	maskSecretData(object)

	values, _, _ := unstructuredv1.NestedMap(object.Object, "data")
	if nested, isMap := values["nested"].(map[string]any); !isMap || nested["inner"] != "value" {
		t.Errorf("nested = %#v, want it untouched: it is not a value shape", values["nested"])
	}
	if _, isSlice := values["listy"].([]any); !isSlice {
		t.Errorf("listy = %#v, want it untouched", values["listy"])
	}
	// And the real value beside them is still hidden.
	if got, _ := values["pw"].(string); got != "<hidden, 8 bytes>" {
		t.Errorf("pw = %q, one odd key must not stop the others being masked", got)
	}
}

// toStringForTest renders whatever a masked slot holds, so an assertion can
// look for a leaked number without caring which type survived.
func toStringForTest(value any) string {
	if text, isString := value.(string); isString {
		return text
	}
	return ""
}

func TestMaskSecretDataSurvivesValuesItCannotDecode(t *testing.T) {
	t.Parallel()

	// A value the API server would never produce, but which must not cause
	// the real values beside it to be returned in the clear.
	object := secretObject(map[string]any{
		"broken": "not-valid-base64!!!",
		"pw":     "cGFzc3dvcmQ=",
	})

	maskSecretData(object)

	values, _, _ := unstructuredv1.NestedMap(object.Object, "data")
	if got, _ := values["broken"].(string); !strings.HasPrefix(got, "<hidden,") {
		t.Errorf("broken = %q, want it hidden even though its length is unknown", got)
	}
	if got, _ := values["pw"].(string); strings.Contains(got, "cGFzc3dvcmQ") {
		t.Errorf("pw = %q, one bad key must not expose the others", got)
	}
}
