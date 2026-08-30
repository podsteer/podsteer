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
