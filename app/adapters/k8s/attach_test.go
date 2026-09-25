package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newHTTPTestAdapter returns an Adapter whose factory resolves id against a
// real HTTP server, through the SAME kubeconfig-driven path a real cluster
// uses.
//
// newTestAdapter (management_test.go) wires a fake clientset instead, which
// is right for the calls that only need typed objects in and out — but
// AttachToPod and ExecInPodWithTTY build a raw *rest.Request and hand it to
// remotecommand.NewSPDYExecutor, which issues a real HTTP request the fake
// clientset never sends anywhere. Only a genuine rest.Config pointing at a
// genuine listener proves what URL that request actually carries.
func newHTTPTestAdapter(t *testing.T, id domain.ClusterID, server *httptest.Server) *Adapter {
	t.Helper()

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: %s
current-context: %s
users:
- name: test
  user: {}
`, server.URL, id, id)

	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}

	return &Adapter{
		factory: newClientFactory(Config{KubeconfigPath: path}),
		logger:  slog.New(slog.DiscardHandler),
	}
}

// attachablePod is a pod with one container whose spec declares both tty and
// stdin — the minimum AttachToPod requires before it will build a request at
// all.
func attachablePod(namespace, name, container string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container, TTY: true, Stdin: true}},
		},
	}
}

func writeJSONPod(t *testing.T, w http.ResponseWriter, pod *corev1.Pod) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pod); err != nil {
		t.Fatalf("encoding pod response: %v", err)
	}
}

// TestAttachToPodRequestsTheAttachSubresource pins the URL AttachToPod
// builds: the attach subresource, naming the pod, namespace and container,
// asking for a tty. It cannot observe a real SPDY upgrade — that needs a
// cluster — so the fake server refuses to upgrade, which is enough: the
// request has already reached it and been recorded by then.
func TestAttachToPodRequestsTheAttachSubresource(t *testing.T) {
	var gotMethod, gotPath, gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/"):
			writeJSONPod(t, w, attachablePod("default", "web-0", "app"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attach"):
			gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
			// No upgrade: StreamWithContext fails here, and that failure is
			// exactly what the test expects — the assertions are on what was
			// already sent, not on what a real cluster would do next.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.AttachToPod(context.Background(), "dev", "default", "web-0", "app", nil, io.Discard, io.Discard, nil)
	if err == nil {
		t.Fatal("AttachToPod() error = nil, want the upgrade failure the fake server forces")
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("request method = %q, want POST — the attach request was never sent", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/attach") {
		t.Fatalf("request path = %q, want it to end in /attach", gotPath)
	}
	if !strings.Contains(gotPath, "/namespaces/default/pods/web-0/") {
		t.Fatalf("request path = %q, want it to name namespace default and pod web-0", gotPath)
	}

	values, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parsing query %q: %v", gotQuery, err)
	}
	want := map[string]string{
		"container": "app",
		"stdout":    "true",
		"stderr":    "true",
		"tty":       "true",
	}
	for key, wantValue := range want {
		if got := values.Get(key); got != wantValue {
			t.Errorf("query[%q] = %q, want %q (full query = %q)", key, got, wantValue, gotQuery)
		}
	}
	// AttachToPod was called with a nil stdin reader, so Stdin is false —
	// which PodAttachOptions' own `omitempty` tag drops from the query
	// entirely rather than encoding "false".
	if got := values.Get("stdin"); got != "" && got != "false" {
		t.Errorf("query[stdin] = %q, want unset or false — the call passed a nil stdin", got)
	}
}

// TestAttachToPodRefusesATTYlessContainer pins the local refusal: a
// container whose own spec has tty:false must never reach the cluster,
// because Kubernetes' own failure for that case is a confusing one that
// arrives only once the PTY negotiation begins.
func TestAttachToPodRefusesATTYlessContainer(t *testing.T) {
	attachReached := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/"):
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "default"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", TTY: false, Stdin: true}},
				},
			}
			writeJSONPod(t, w, pod)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attach"):
			attachReached = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.AttachToPod(context.Background(), "dev", "default", "web-0", "app", nil, io.Discard, io.Discard, nil)
	if !errors.Is(err, domain.ErrContainerNotAttachable) {
		t.Fatalf("AttachToPod() error = %v, want wrapping domain.ErrContainerNotAttachable", err)
	}
	if attachReached {
		t.Fatal("the attach request reached the server — a tty-less container must be refused locally")
	}
}

// TestAttachToPodRefusesAContainerWithNoStdin covers the other half of the
// same guard: tty without stdin lets a process render but never receive a
// keystroke, which is not what Attach promises either.
func TestAttachToPodRefusesAContainerWithNoStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/") {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "default"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", TTY: true, Stdin: false}},
				},
			}
			writeJSONPod(t, w, pod)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.AttachToPod(context.Background(), "dev", "default", "web-0", "app", nil, io.Discard, io.Discard, nil)
	if !errors.Is(err, domain.ErrContainerNotAttachable) {
		t.Fatalf("AttachToPod() error = %v, want wrapping domain.ErrContainerNotAttachable", err)
	}
}

// TestAttachToPodRefusesAMissingContainer covers a container name that does
// not exist in the pod at all — distinct from one that exists but lacks a
// tty, and reported as ports.ErrNotFound rather than the attachability error.
func TestAttachToPodRefusesAMissingContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/") {
			writeJSONPod(t, w, attachablePod("default", "web-0", "app"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.AttachToPod(context.Background(), "dev", "default", "web-0", "does-not-exist", nil, io.Discard, io.Discard, nil)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("AttachToPod() error = %v, want wrapping ports.ErrNotFound", err)
	}
}
