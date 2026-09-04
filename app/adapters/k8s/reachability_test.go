package k8s

import (
	"errors"
	"net/http"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// statusError builds the shape the API server actually returns for a proxied
// request, which is what classifyProxyOutcome has to read.
func statusError(code int32, reason metav1.StatusReason, message string) error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    code,
		Reason:  reason,
		Message: message,
	}}
}

func stepStatus(t *testing.T, observation domain.ProbeObservation, name domain.ProbeStepName) domain.ProbeStatus {
	t.Helper()
	for _, step := range observation.Steps {
		if step.Name == name {
			return step.Status
		}
	}
	return ""
}

// The three cases that matter, and telling them apart is the whole job: the
// account being refused the subresource is not an answer about the Service,
// the API server failing to dial is a refusal, and any status the SERVICE
// answered means it is up.
func TestClassifyProxyOutcomeTellsARefusedSubresourceFromAnAnsweredRequest(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantErr     bool
		wantConnect domain.ProbeStatus
		wantCode    int
	}{
		{
			name:        "the proxy answered",
			err:         nil,
			wantConnect: domain.StatusOK,
			wantCode:    http.StatusOK,
		},
		{
			name: "RBAC declined the proxy subresource, which says nothing about the service",
			err: statusError(http.StatusForbidden, metav1.StatusReasonForbidden,
				`services "web" is forbidden: User "dev" cannot get resource "services/proxy" in API group "" in the namespace "shop"`),
			wantErr: true,
		},
		{
			name: "the API server could not reach the endpoints, which is a refused connection",
			err: statusError(http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable,
				"error trying to reach service: dial tcp 10.1.2.3:80: connect: connection refused"),
			wantConnect: domain.StatusFailed,
		},
		{
			name:        "the service itself answered 404, which means it accepted the connection",
			err:         statusError(http.StatusNotFound, metav1.StatusReasonNotFound, "404 page not found"),
			wantConnect: domain.StatusOK,
			wantCode:    http.StatusNotFound,
		},
		{
			name:        "the service itself answered 500, which still means it is up",
			err:         statusError(http.StatusInternalServerError, metav1.StatusReasonInternalError, "upstream exploded"),
			wantConnect: domain.StatusOK,
			wantCode:    http.StatusInternalServerError,
		},
		{
			name: "the service itself answered 403 without naming a subresource, so it is an answer",
			err: &apierrors.StatusError{ErrStatus: metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusForbidden,
				Reason: metav1.StatusReasonForbidden, Message: "signature required",
			}},
			wantConnect: domain.StatusOK,
			wantCode:    http.StatusForbidden,
		},
		{
			name:    "a transport failure is not an answer about anything",
			err:     errors.New("dial tcp: lookup api.example.com: no such host"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation, err := classifyProxyOutcome("probing", tt.err, 3*time.Millisecond)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected the probe to report that it could not be performed")
				}
				if len(observation.Steps) != 0 {
					t.Errorf("a probe that could not be performed must carry no steps, got %v", observation.Steps)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got := stepStatus(t, observation, domain.StepConnect); got != tt.wantConnect {
				t.Errorf("connect = %q, want %q", got, tt.wantConnect)
			}
			if observation.StatusCode != tt.wantCode {
				t.Errorf("status code = %d, want %d", observation.StatusCode, tt.wantCode)
			}
			if got := stepStatus(t, observation, domain.StepDNS); got != domain.StatusSkipped {
				t.Errorf("dns = %q, want skipped — the API server resolved it, not us", got)
			}
		})
	}
}

// A 503 carries the API server's own dial error, and that text names the
// address and the reason. It is the diagnosis, so it travels verbatim.
func TestClassifyProxyOutcomeKeepsTheDialErrorVerbatim(t *testing.T) {
	message := "error trying to reach service: dial tcp 10.1.2.3:80: i/o timeout"
	observation, err := classifyProxyOutcome("probing",
		statusError(http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, message), 0)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	for _, step := range observation.Steps {
		if step.Name == domain.StepConnect {
			if step.Detail != message {
				t.Errorf("detail = %q, want the API server's own words", step.Detail)
			}
			return
		}
	}
	t.Fatal("no connect step")
}

// A refusal of the subresource classifies as forbidden, so the frontend gets
// the sentence it gets for any other RBAC refusal rather than a red cross
// against somebody's Service.
func TestARefusedProxyClassifiesAsForbidden(t *testing.T) {
	err := apierrors.NewForbidden(
		schema.GroupResource{Resource: "services/proxy"}, "web",
		errors.New(`User "dev" cannot get resource "services/proxy"`))

	_, classified := classifyProxyOutcome("probing", err, 0)
	if !errors.Is(classified, ports.ErrForbidden) {
		t.Fatalf("classified = %v, want ports.ErrForbidden", classified)
	}
}

// The same shape of test tarMissing has, and for the same reason: an operator
// reading "exit status 126" and guessing is the experience this exists to end.
func TestShellMissingRecognisesAContainerWithNoShell(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stderr string
		want   bool
	}{
		{
			name: "containerd cannot start the process",
			err:  errors.New(`failed to exec in container: OCI runtime exec failed: exec: "/bin/sh": executable file not found in $PATH`),
			want: true,
		},
		{
			name:   "a wrapper writes to stderr instead",
			stderr: "/bin/sh: no such file or directory",
			want:   true,
		},
		{
			name: "a command that could not be invoked",
			err:  utilexec.CodeExitError{Err: errors.New("exit"), Code: 126},
			want: true,
		},
		{
			name:   "an ordinary non-zero exit is not a missing shell",
			err:    utilexec.CodeExitError{Err: errors.New("exit"), Code: 1},
			stderr: "nc: bad address",
			want:   false,
		},
		{
			name: "nothing at all",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellMissing(tt.err, tt.stderr); got != tt.want {
				t.Errorf("shellMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}
