// Package vendorcli runs a cloud CLI the operator already has.
//
// WHAT IT IS ALLOWED TO DO, AND NOTHING MORE. It looks for a binary on PATH,
// runs it with an argv the domain composed, reads a bounded amount of its
// stdout, and hands back what it printed. It contacts nothing, reads no
// credential, and decides nothing about which command to run — see
// domain/vendorcli.go, where the plan and the parser live together because the
// command and its output are one protocol.
//
// A SIBLING OF localshell, NOT A PART OF THE KUBERNETES ADAPTER. Nothing here
// touches a cluster, holds a client or knows what a Kubernetes object is;
// putting process execution inside the adapter that holds every credential
// path would widen that package's job for no reason. It is also the single
// most important thing to keep structurally out of the MCP surface, which is
// handed narrow readers rather than whole adapters.
//
// Every refusal in this file is deliberate and recorded in decision 12: no
// shell, an absolute path from PATH only, a binary nobody else can rewrite, a
// bound on time and a bound on output, stderr captured but never read as data,
// and an environment passed through but never logged.
package vendorcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// stderrTail is how much of a CLI's complaint is kept.
//
// Enough for the sentence that matters and the two around it. A CLI having a
// bad day can write a great deal, and the whole of it in an error is an error
// nobody reads.
const stderrTail = 8 << 10

// Runner drives the cloud CLIs the table describes.
type Runner struct {
	logger *slog.Logger

	// mu guards running.
	mu sync.Mutex
	// running holds one cancel per provider with a run in the air, so a
	// listing can be stopped from the interface.
	//
	// COMPARED BY IDENTITY when it is removed, the rule ClusterAPI.connecting
	// states: two runs for one provider can overlap when somebody cancels and
	// presses again, and a plain delete would have the first run's cleanup
	// remove the second run's entry, leaving a run nothing can stop.
	running map[string]*run
}

type run struct{ cancel context.CancelFunc }

// New returns a runner.
func New(logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		logger:  logger.With(slog.String("adapter", "vendorcli")),
		running: make(map[string]*run, 2),
	}
}

// Providers reports every row in the table and whether its binary was found.
//
// NO PROCESS IS STARTED. This is exec.LookPath and nothing else — the same
// thing DetectAgents does for coding agents, and for the same reason: an
// operator opening a dialog has not asked PodSteer to run anything.
func (r *Runner) Providers() []domain.VendorCLIStatus {
	clis, err := domain.VendorCLIs()
	if err != nil {
		r.logger.Error("the cloud CLI table did not load", slog.String("error", err.Error()))
		return nil
	}

	out := make([]domain.VendorCLIStatus, 0, len(clis))
	for _, cli := range clis {
		status := domain.VendorCLIStatus{VendorCLI: cli}
		if path, err := resolve(cli.Binary); err == nil {
			status.Path = path
			status.Installed = true
		}
		out = append(out, status)
	}
	return out
}

// ListClusters runs one provider's listing command.
func (r *Runner) ListClusters(ctx context.Context, provider string) (domain.VendorClusterList, error) {
	plan, err := domain.PlanVendorList(provider)
	if err != nil {
		return domain.VendorClusterList{}, err
	}

	output, stderr, err := r.execute(ctx, provider, plan, "")
	if err != nil {
		if errors.Is(err, ports.ErrVendorCLIDeclined) {
			// NOT AN ERROR TO THE CALLER. A CLI that will not answer is a
			// state this feature is about, not a failure of the call: the
			// interface shows the CLI's own words and offers to try again.
			// Only a failure to RUN it — missing, timed out, unreadable — is
			// returned as an error.
			return domain.VendorClusterList{
				Provider: provider,
				Status:   domain.VendorDeclined,
				Reason:   stderr,
			}, nil
		}
		return domain.VendorClusterList{}, err
	}

	clusters, err := domain.ParseVendorList(provider, output)
	if err != nil {
		return domain.VendorClusterList{}, fmt.Errorf("%w: %w", ports.ErrVendorCLIUnreadable, err)
	}

	return domain.VendorClusterList{
		Provider: provider,
		Status:   domain.VendorListed,
		Clusters: clusters,
	}, nil
}

// WriteKubeconfig has the CLI write an entry, and returns what it wrote.
//
// INTO A FILE PODSTEER OWNS AND THEN DELETES, never the operator's own. Both
// of these CLIs set `current-context` when they write a kubeconfig, and the
// operator's current context belongs to them — a local terminal here goes to
// the length of a separate overlay file rather than change it. The text comes
// back as a string and is merged by the one code path that has ever written
// that file, keeping the backup, the atomic write and the conflict refusal.
func (r *Runner) WriteKubeconfig(ctx context.Context, provider string, cluster domain.VendorCluster) (string, error) {
	// THE BEST CASE IS NO FILE AT ALL. Some of these CLIs print the kubeconfig
	// rather than writing one, and for those the cost decision 12 names and
	// accepts — a temporary file that can briefly hold credential material —
	// is simply not paid: nothing reaches disk on the way through.
	prints, err := domain.VendorPrintsKubeconfig(provider)
	if err != nil {
		return "", err
	}
	if prints {
		plan, err := domain.PlanVendorAdd(provider, cluster, "")
		if err != nil {
			return "", err
		}
		printed, stderr, err := r.execute(ctx, provider, plan, "")
		if err != nil {
			if errors.Is(err, ports.ErrVendorCLIDeclined) {
				return "", fmt.Errorf("%w: %s", ports.ErrVendorCLIDeclined, stderr)
			}
			return "", err
		}
		if len(bytes.TrimSpace(printed)) == 0 {
			return "", fmt.Errorf("%w: it exited cleanly and printed no kubeconfig", ports.ErrVendorCLIUnreadable)
		}
		return string(printed), nil
	}

	dir, err := os.MkdirTemp("", "podsteer-kubeconfig-")
	if err != nil {
		return "", fmt.Errorf("making a directory for the CLI to write into: %w", err)
	}
	// REMOVED ON EVERY PATH, including the failures. For some subcommands this
	// file can hold credential material, which is the cost decision 12 names
	// and accepts; leaving one behind would turn a moment into a leak.
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("securing the directory for the CLI to write into: %w", err)
	}

	path := filepath.Join(dir, "config")
	plan, err := domain.PlanVendorAdd(provider, cluster, path)
	if err != nil {
		return "", err
	}

	if _, stderr, err := r.execute(ctx, provider, plan, path); err != nil {
		if errors.Is(err, ports.ErrVendorCLIDeclined) {
			return "", fmt.Errorf("%w: %s", ports.ErrVendorCLIDeclined, stderr)
		}
		return "", err
	}

	written, err := os.ReadFile(path)
	if err != nil {
		// The CLI exited cleanly and wrote nothing where it was told to. That
		// is the CLI's contract broken rather than the operator's problem, and
		// it must not read as "your cluster could not be added".
		return "", fmt.Errorf("%w: it exited cleanly but wrote no kubeconfig", ports.ErrVendorCLIUnreadable)
	}
	return string(written), nil
}

// Cancel stops a run in the air, if there is one.
func (r *Runner) Cancel(provider string) {
	r.mu.Lock()
	current, ok := r.running[provider]
	r.mu.Unlock()

	if ok {
		current.cancel()
	}
}

// execute runs one plan and returns its stdout and the tail of its stderr.
func (r *Runner) execute(ctx context.Context, provider string, plan domain.VendorPlan, kubeconfigPath string) ([]byte, string, error) {
	binary, err := resolve(plan.Binary)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %q", ports.ErrVendorCLIMissing, plan.Binary)
	}
	if err := refuseUnsafePath(binary); err != nil {
		return nil, "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	current := &run{cancel: cancel}
	r.begin(provider, current)
	defer r.end(provider, current)

	cmd := exec.CommandContext(runCtx, binary, plan.Args...)
	cmd.Env = environment(plan, kubeconfigPath)

	// STDIN IS CLOSED, NOT INHERITED. A CLI that decides to ask something gets
	// EOF and gives up, where an inherited stdin would leave it waiting for a
	// terminal that does not exist — the same choice shellpath makes when it
	// probes the login shell.
	cmd.Stdin = nil

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{to: &stdout, limit: domain.VendorOutputLimit}
	cmd.Stderr = &limitedWriter{to: &stderr, limit: stderrTail}
	setProcessGroup(cmd)

	err = cmd.Run()
	tail := strings.TrimSpace(stderr.String())

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, tail, fmt.Errorf("%w: %s", ports.ErrVendorCLITimedOut, tail)
	case runCtx.Err() != nil:
		return nil, tail, runCtx.Err()
	case err != nil:
		// A NON-ZERO EXIT IS THE CLI'S ANSWER, not a fault of PodSteer's. The
		// caller decides what that means; here it only stops being a success.
		r.logger.Debug("cloud CLI declined",
			slog.String("provider", provider), slog.String("stderr", tail))
		return nil, tail, ports.ErrVendorCLIDeclined
	}

	if stdout.Len() >= domain.VendorOutputLimit {
		return nil, tail, fmt.Errorf("%w: it printed more than a listing can be", ports.ErrVendorCLIUnreadable)
	}

	// Chatter on stderr from a successful run — update notices, deprecation
	// warnings — is logged and never read as evidence of failure.
	if tail != "" {
		r.logger.Debug("cloud CLI wrote to stderr and succeeded anyway",
			slog.String("provider", provider), slog.String("stderr", tail))
	}
	return stdout.Bytes(), tail, nil
}

func (r *Runner) begin(provider string, current *run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[provider] = current
}

func (r *Runner) end(provider string, current *run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// By identity: a later run's entry must survive an earlier run's cleanup.
	if r.running[provider] == current {
		delete(r.running, provider)
	}
}

// environment is what the child is given.
//
// THE OPERATOR'S OWN ENVIRONMENT, because the whole design is that this CLI
// authenticates as them, with the configuration they already have. PodSteer
// does not read those variables and nothing here logs them — passing is not
// reading, but a log line that captured them would make that distinction one
// nobody believes.
func environment(plan domain.VendorPlan, kubeconfigPath string) []string {
	env := os.Environ()
	for key, value := range plan.Env {
		env = append(env, key+"="+value)
	}
	if plan.KubeconfigEnv != "" && kubeconfigPath != "" {
		env = append(env, plan.KubeconfigEnv+"="+kubeconfigPath)
	}
	return env
}

// limitedWriter stops accepting past a bound, without failing the command.
//
// The write does not error, so the CLI is not killed by a broken pipe halfway
// through saying something useful — it simply cannot make PodSteer hold more
// than the bound. What it printed past the limit is the caller's to refuse.
type limitedWriter struct {
	to    *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	room := w.limit - w.to.Len()
	if room <= 0 {
		return len(p), nil
	}
	if len(p) > room {
		w.to.Write(p[:room])
		return len(p), nil
	}
	w.to.Write(p)
	return len(p), nil
}

var _ io.Writer = (*limitedWriter)(nil)
