package k8s

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// recordExec returns a server that records the one exec request it
// receives and refuses to upgrade it, exactly as attach_test.go's servers
// do: the request has already been sent by then, and its URL is what these
// tests are about.
func recordExec(t *testing.T) (*httptest.Server, *url.Values) {
	t.Helper()

	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec") {
			query = r.URL.Query()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server, &query
}

// TestCopyFromPodRunsTarAgainstTheExecSubresource pins the exact command
// line a download sends — kubectl cp's, with the archive rooted at the
// entry's own name — and that the stream asks for stdout and stderr and
// nothing else.
func TestCopyFromPodRunsTarAgainstTheExecSubresource(t *testing.T) {
	server, query := recordExec(t)
	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.CopyFromPod(context.Background(), "dev", "default", "web-0", "app", "/etc/nginx/nginx.conf", io.Discard)
	if err == nil {
		t.Fatal("CopyFromPod() error = nil, want the upgrade failure the fake server forces")
	}
	if *query == nil {
		t.Fatal("the exec request was never sent")
	}

	want := []string{"tar", "cf", "-", "-C", "/etc/nginx", "nginx.conf"}
	if got := (*query)["command"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	if got := query.Get("container"); got != "app" {
		t.Fatalf("container = %q, want app", got)
	}
	if query.Get("stdout") != "true" || query.Get("stderr") != "true" {
		t.Fatalf("stdout/stderr = %q/%q, want both true", query.Get("stdout"), query.Get("stderr"))
	}
	if stdin := query.Get("stdin"); stdin != "" && stdin != "false" {
		t.Fatalf("stdin = %q, want unset — a download sends nothing", stdin)
	}
	if tty := query.Get("tty"); tty != "" && tty != "false" {
		t.Fatalf("tty = %q, want unset — a TTY would corrupt the archive with line endings", tty)
	}
}

// TestCopyToPodRunsTarExtractWithStdin is the upload half: extract into the
// destination, reading the archive from stdin, with no stdout attached.
func TestCopyToPodRunsTarExtractWithStdin(t *testing.T) {
	server, query := recordExec(t)
	adapter := newHTTPTestAdapter(t, "dev", server)

	err := adapter.CopyToPod(context.Background(), "dev", "default", "web-0", "app", "/app/data/", strings.NewReader(""))
	if err == nil {
		t.Fatal("CopyToPod() error = nil, want the upgrade failure the fake server forces")
	}
	if *query == nil {
		t.Fatal("the exec request was never sent")
	}

	want := []string{"tar", "xf", "-", "-C", "/app/data"}
	if got := (*query)["command"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	if query.Get("stdin") != "true" || query.Get("stderr") != "true" {
		t.Fatalf("stdin/stderr = %q/%q, want both true", query.Get("stdin"), query.Get("stderr"))
	}
	if stdout := query.Get("stdout"); stdout != "" && stdout != "false" {
		t.Fatalf("stdout = %q, want unset — tar xf has nothing to say on success", stdout)
	}
}

// TestCopyFromPodRefusesAnInvalidPathBeforeAnyRequest: the root, or
// nothing, never costs a round trip.
func TestCopyFromPodRefusesAnInvalidPathBeforeAnyRequest(t *testing.T) {
	server, query := recordExec(t)
	adapter := newHTTPTestAdapter(t, "dev", server)

	for _, remote := range []string{"/", "", "../x"} {
		err := adapter.CopyFromPod(context.Background(), "dev", "default", "web-0", "app", remote, io.Discard)
		if !errors.Is(err, domain.ErrInvalidRemotePath) {
			t.Errorf("CopyFromPod(%q) error = %v, want ErrInvalidRemotePath", remote, err)
		}
	}
	if *query != nil {
		t.Fatal("an invalid path reached the server")
	}
}

// TestTarMissingIsRecognisedInEveryWordingRuntimesUse covers the signals a
// missing tar arrives as, and the ordinary tar failures that must NOT be
// mistaken for one.
func TestTarMissingIsRecognisedInEveryWordingRuntimesUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		stderr string
		want   bool
	}{
		{
			name: "containerd refuses to start the process",
			err: apierrors.NewInternalError(errors.New(
				`error executing command in container: failed to exec in container: failed to start exec "abc": OCI runtime exec failed: exec failed: unable to start container process: exec: "tar": executable file not found in $PATH: unknown`)),
			want: true,
		},
		{
			name: "cri-o names the file",
			err:  errors.New(`exec failed: unable to start container process: exec: "tar": no such file or directory`),
			want: true,
		},
		{
			name: "a shell wrapper exits 127",
			err:  utilexec.CodeExitError{Err: errors.New("command terminated with exit code 127"), Code: 127},
			want: true,
		},
		{
			name:   "busybox sh says not found",
			err:    utilexec.CodeExitError{Err: errors.New("command terminated with exit code 2"), Code: 2},
			stderr: "sh: tar: not found",
			want:   true,
		},
		{
			name:   "bash says command not found",
			stderr: "bash: tar: command not found",
			want:   true,
		},
		{
			name:   "tar itself failing is not tar missing",
			err:    utilexec.CodeExitError{Err: errors.New("command terminated with exit code 2"), Code: 2},
			stderr: "tar: /nope: Cannot stat: No such file or directory\ntar: Exiting with failure status due to previous errors",
			want:   false,
		},
		{
			name: "a forbidden exec is a permission problem",
			err:  apierrors.NewForbidden(schemaGroupResource("pods"), "web-0", errors.New("no")),
			want: false,
		},
		{
			name: "nothing at all",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tarMissing(tt.err, tt.stderr); got != tt.want {
				t.Fatalf("tarMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCommandOutcomeReportsAMissingTarByName: its own sentinel, so the UI
// can say exactly what is wrong rather than quoting an exit code.
func TestCommandOutcomeReportsAMissingTarByName(t *testing.T) {
	t.Parallel()

	adapter := &Adapter{logger: discardLogger()}
	err := adapter.commandOutcome(context.Background(), "copying", utilexec.CodeExitError{Err: errors.New("exit 127"), Code: 127}, "sh: tar: not found")

	if !errors.Is(err, ports.ErrTarMissing) {
		t.Fatalf("error = %v, want ErrTarMissing", err)
	}
	if errors.Is(err, ports.ErrCommandFailed) {
		t.Fatal("a missing tar must not also read as an ordinary command failure")
	}
}

// TestCommandOutcomeCarriesStderrVerbatim: what tar said is the diagnosis
// and reaches the error unparaphrased.
func TestCommandOutcomeCarriesStderrVerbatim(t *testing.T) {
	t.Parallel()

	const said = "tar: /nope: Cannot stat: No such file or directory"
	adapter := &Adapter{logger: discardLogger()}
	err := adapter.commandOutcome(context.Background(), "copying", utilexec.CodeExitError{Err: errors.New("exit 2"), Code: 2}, said+"\n")

	if !errors.Is(err, ports.ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
	if !strings.Contains(err.Error(), said) {
		t.Fatalf("error %q does not carry tar's own words %q", err, said)
	}
}

// TestCommandOutcomeSucceedsDespiteWarnings: a non-empty stderr with a zero
// exit is a successful transfer with something to note, not a failure.
func TestCommandOutcomeSucceedsDespiteWarnings(t *testing.T) {
	t.Parallel()

	adapter := &Adapter{logger: discardLogger()}
	if err := adapter.commandOutcome(context.Background(), "copying", nil, "tar: Removing leading `/' from member names"); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

// TestCommandOutcomeReportsCancellationAsSuch: a killed command's stderr is
// not a diagnosis, whatever it says.
func TestCommandOutcomeReportsCancellationAsSuch(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	adapter := &Adapter{logger: discardLogger()}
	err := adapter.commandOutcome(ctx, "copying", utilexec.CodeExitError{Err: errors.New("exit 137"), Code: 137}, "tar: broken pipe")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ports.ErrCommandFailed) {
		t.Fatal("a cancelled command must not read as a failed one")
	}
}

// TestCommandOutcomeClassifiesAnAPIRefusal: an exec the API server refused
// goes through the same classification every other call does, with tar's
// stderr appended if there was any.
func TestCommandOutcomeClassifiesAnAPIRefusal(t *testing.T) {
	t.Parallel()

	adapter := &Adapter{logger: discardLogger()}
	err := adapter.commandOutcome(context.Background(), "copying",
		apierrors.NewForbidden(schemaGroupResource("pods"), "web-0", errors.New("no")), "")
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

// TestCappedBufferKeepsTheFirstBytesAndNeverFails pins the shape stderr is
// captured into: bounded, and never a write error the stream could stop on.
func TestCappedBufferKeepsTheFirstBytesAndNeverFails(t *testing.T) {
	t.Parallel()

	buf := newCappedBuffer(5)
	for _, chunk := range []string{"abc", "def", "ghi"} {
		n, err := buf.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v; want full length and nil", chunk, n, err)
		}
	}
	if got := buf.String(); got != "abcde" {
		t.Fatalf("String() = %q, want the first five bytes", got)
	}

	if got := excerpt("abcdef", 3); got != "abc…" {
		t.Fatalf("excerpt() = %q", got)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func schemaGroupResource(resource string) schema.GroupResource {
	return schema.GroupResource{Resource: resource}
}
