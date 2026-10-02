package wails

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

func TestFormatAgeMatchesTheFrontend(t *testing.T) {
	t.Parallel()

	const day = 86400
	tests := []struct {
		seconds float64
		want    string
	}{
		{-1, "—"},
		{0, "0s"},
		{45, "45s"},
		{90, "1m30s"},
		{600, "10m"},
		{661, "11m"},
		{3600, "1h"},
		{3660, "1h1m"},
		{day, "1d"},
		{day + 3600, "1d1h"},
		{10*day + 3600, "10d"},
		{365 * day, "1y"},
		{366 * day, "1y1d"},
	}
	for _, tt := range tests {
		if got := formatAge(tt.seconds); got != tt.want {
			t.Errorf("formatAge(%v) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

// quantity is the frontend's parseQuantity, enough of it for memory.
var quantity = regexp.MustCompile(`^([\d.]+)(KiB|MiB|GiB|TiB|PiB|B)?$`)

// TestDisplayedBytesIsWhatTheTableShows holds domain.DisplayedBytes — what
// the pod sort compares memory by — to this package's formatBytes, which is
// what the table displays. They are two functions in two layers saying one
// thing, and a sort that read a different number than the cell shows would
// order rows the operator sees as equal.
func TestDisplayedBytesIsWhatTheTableShows(t *testing.T) {
	t.Parallel()

	multiplier := map[string]float64{"": 1, "B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "PiB": 1 << 50}
	for _, bytes := range []int64{0, 1, 12, 1023, 1024, 1500, 268_435_456, 268_475_456, 1_288_490_188, 5 << 40, 3 << 50} {
		shown := formatBytes(bytes)
		got := domain.DisplayedBytes(bytes)

		match := quantity.FindStringSubmatch(shown)
		if match == nil {
			if got != nil {
				t.Errorf("%d bytes shows %q but sorts as %v, want unmeasured", bytes, shown, *got)
			}
			continue
		}
		number, _ := strconv.ParseFloat(match[1], 64)
		want := number * multiplier[match[2]]
		if got == nil || *got != want {
			t.Errorf("%d bytes shows %q (%v) but sorts as %v", bytes, shown, want, got)
		}
	}
}

func TestRenderPodCSVIsTheTablesText(t *testing.T) {
	t.Parallel()

	pod := Pod{
		Name:         "=cmd",
		Namespace:    "shop",
		ClusterID:    "prod",
		Phase:        "Running",
		StatusReason: "CrashLoopBackOff",
		Ready:        "0/1",
		Restarts:     3,
		CPU:          "0.120",
		Memory:       "256.0MiB",
		AgeSeconds:   90,
		Labels:       map[string]string{"team": `pay, "core"`},
		Custom:       map[string]string{"jsonpath:.spec.priorityClassName": "high"},
	}
	columns := []CSVColumn{
		{ID: "status", Label: "Status"},
		{ID: "cluster", Label: "Cluster"},
		{ID: "name", Label: "Name"},
		{ID: "cpu", Label: "CPU"},
		{ID: "restarts", Label: "Restarts"},
		{ID: "node", Label: "Node"},
		{ID: "age", Label: "Age"},
		{ID: "label:team", Label: "team"},
		{ID: "annotation:owner", Label: "owner"},
		{ID: "jsonpath:.spec.priorityClassName", Label: "+priority"},
	}

	want := "Status,Cluster,Name,CPU,Restarts,Node,Age,team,owner,'+priority\r\n" +
		`CrashLoopBackOff,prod,'=cmd,0.120,3,—,1m30s,"pay, ""core""",—,high` + "\r\n"
	if got := renderPodCSV(columns, []Pod{pod}); got != want {
		t.Fatalf("CSV\n got %q\nwant %q", got, want)
	}
}

// exportingWorkloads answers MatchingPods and nothing else.
type exportingWorkloads struct {
	ports.WorkloadService
	pods  []domain.Pod
	query domain.PodQuery
}

func (e *exportingWorkloads) MatchingPods(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, _ domain.Projection, query domain.PodQuery) ([]domain.Pod, error) {
	e.query = query
	return e.pods, nil
}

func TestExportPodsCSVWritesEveryMatchWhereTheOperatorChose(t *testing.T) {
	t.Parallel()

	pod, err := domain.NewPod(domain.PodSpec{Name: "web-1", Namespace: "shop", ClusterID: "dev", Phase: domain.PodPhaseRunning})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	workloads := &exportingWorkloads{pods: []domain.Pod{pod}}
	api, err := NewWorkloadAPI(workloads, NewApp(slog.Default(), 0), slog.Default())
	if err != nil {
		t.Fatalf("NewWorkloadAPI() error = %v", err)
	}

	target := filepath.Join(t.TempDir(), "pods.csv")
	var suggested string
	api.chooseSavePath = func(name string) (string, error) {
		suggested = name
		return target, nil
	}

	path, err := api.ExportPodsCSV("dev", "shop", nil, nil, PodQuery{Text: "web", Limit: 50, Offset: 50}, []CSVColumn{{ID: "name", Label: "Name"}}, "dev-pods.csv")
	if err != nil {
		t.Fatalf("ExportPodsCSV() error = %v", err)
	}
	if path != target || suggested != "dev-pods.csv" {
		t.Fatalf("wrote %q after suggesting %q", path, suggested)
	}
	if workloads.query.Text != "web" {
		t.Fatalf("the export was read with %+v, want the table's search", workloads.query)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the export: %v", err)
	}
	if string(written) != "Name\r\nweb-1\r\n" {
		t.Fatalf("export = %q", written)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %v, want 0600", info.Mode().Perm())
	}

	// Cancelling the dialog writes nothing and is not an error.
	api.chooseSavePath = func(string) (string, error) { return "", nil }
	if path, err := api.ExportPodsCSV("dev", "shop", nil, nil, PodQuery{Limit: 50}, nil, "dev-pods.csv"); err != nil || path != "" {
		t.Fatalf("cancelled export = (%q, %v), want nothing", path, err)
	}
}
