package domain_test

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/adapters/collation"
	"github.com/podsteer/podsteer/app/domain"
)

// The parity test: the fixture the frontend's filter.test.ts runs, run
// against the Go port. Both must produce the fixture's expected rows and chip
// counts; see web/src/lib/filter.test.ts for how the fixture is regenerated
// and why it is the TypeScript that is the reference.

type fixturePod struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	NodeName        string            `json:"nodeName"`
	Phase           string            `json:"phase"`
	StatusReason    string            `json:"statusReason"`
	IsHealthy       bool              `json:"isHealthy"`
	CPU             string            `json:"cpu"`
	Memory          string            `json:"memory"`
	ReadyContainers int               `json:"readyContainers"`
	Restarts        int32             `json:"restarts"`
	ControlledBy    string            `json:"controlledBy"`
	QoSClass        string            `json:"qosClass"`
	PodIP           string            `json:"podIp"`
	AgeSeconds      int64             `json:"ageSeconds"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
	Custom          map[string]string `json:"custom"`
	Containers      []struct {
		LastTermination *struct {
			Reason string `json:"reason"`
		} `json:"lastTermination"`
	} `json:"containers"`
}

type fixtureCase struct {
	Name   string   `json:"name"`
	Search string   `json:"search"`
	Chips  []string `json:"chips"`
	Sort   *struct {
		ColumnID  string `json:"columnId"`
		Direction string `json:"direction"`
	} `json:"sort"`
	Columns []struct {
		Source string `json:"source"`
		Key    string `json:"key"`
	} `json:"columns"`
	Expected   []string       `json:"expected"`
	ChipCounts map[string]int `json:"chipCounts"`
}

type fixture struct {
	Cluster string        `json:"cluster"`
	Pods    []fixturePod  `json:"pods"`
	Cases   []fixtureCase `json:"cases"`
}

// quantity is the webview's parseQuantity, for the fixture's displayed cpu
// and memory strings.
var quantity = regexp.MustCompile(`^([\d.]+)(m|KiB|MiB|GiB|TiB|PiB|Ki|Mi|Gi|Ti|Pi|k|K|M|G|B)?$`)

var multipliers = map[string]float64{
	"": 1, "m": 1e-3, "k": 1e3, "K": 1e3, "M": 1e6, "G": 1e9, "B": 1,
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "PiB": 1 << 50,
}

func parseQuantity(value string) *float64 {
	match := quantity.FindStringSubmatch(value)
	if match == nil {
		return nil
	}
	number, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return nil
	}
	number *= multipliers[match[2]]
	return &number
}

func (p fixturePod) row(cluster string) domain.PodQueryRow {
	row := domain.PodQueryRow{
		Name: p.Name, Namespace: p.Namespace, NodeName: p.NodeName, Phase: p.Phase,
		StatusReason: p.StatusReason, ControlledBy: p.ControlledBy, QoSClass: p.QoSClass,
		PodIP: p.PodIP, Cluster: cluster, IsHealthy: p.IsHealthy,
		ReadyContainers: p.ReadyContainers, Restarts: p.Restarts, AgeSeconds: p.AgeSeconds,
		CPU: parseQuantity(p.CPU), Memory: parseQuantity(p.Memory),
		Labels: p.Labels, Annotations: p.Annotations, Custom: p.Custom,
	}
	for _, container := range p.Containers {
		if container.LastTermination != nil {
			row.LastTerminationReasons = append(row.LastTerminationReasons, container.LastTermination.Reason)
		}
	}
	return row
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("../../web/src/lib/filter.fixtures.json")
	if err != nil {
		t.Fatalf("reading the shared fixture: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decoding the shared fixture: %v", err)
	}
	return f
}

func TestPodQueryMatchesTheFrontendFixture(t *testing.T) {
	t.Parallel()

	f := loadFixture(t)
	rows := make([]domain.PodQueryRow, len(f.Pods))
	for i, pod := range f.Pods {
		rows[i] = pod.row(f.Cluster)
	}

	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			q := domain.PodQuery{Text: c.Search, Offset: 0, Limit: domain.MaxPodPageSize}
			for _, chip := range c.Chips {
				q.Chips = append(q.Chips, domain.PodStatusChip(chip))
			}
			if c.Sort != nil {
				q.SortColumn = c.Sort.ColumnID
				q.Descending = c.Sort.Direction == "desc"
			}
			for _, column := range c.Columns {
				q.Columns = append(q.Columns, domain.CustomColumn{Source: domain.CustomColumnSource(column.Source), Key: column.Key})
			}

			order, counts := domain.RunPodQueryForTest(slices.Clone(rows), q, collation.Weight)

			got := make([]string, 0, len(order))
			for _, index := range order {
				got = append(got, f.Pods[index].Namespace+"/"+f.Pods[index].Name)
			}
			if !slices.Equal(got, c.Expected) {
				t.Errorf("rows\n got %v\nwant %v", got, c.Expected)
			}
			for chip, want := range c.ChipCounts {
				if counts[domain.PodStatusChip(chip)] != want {
					t.Errorf("chip %s count = %d, want %d", chip, counts[domain.PodStatusChip(chip)], want)
				}
			}
		})
	}
}

func TestNewPodQueryRefusesAPageThatIsTheWholeList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query domain.PodQuery
		ok    bool
	}{
		{"default page", domain.PodQuery{Limit: 50}, true},
		{"largest page", domain.PodQuery{Limit: domain.MaxPodPageSize}, true},
		{"no page size", domain.PodQuery{}, false},
		{"over the cap", domain.PodQuery{Limit: domain.MaxPodPageSize + 1}, false},
		{"negative offset", domain.PodQuery{Limit: 50, Offset: -1}, false},
		{"a valid custom column", domain.PodQuery{Limit: 50, Columns: []domain.CustomColumn{{Source: "label", Key: "team"}}}, true},
		{"an unknown source", domain.PodQuery{Limit: 50, Columns: []domain.CustomColumn{{Source: "env", Key: "x"}}}, false},
		{"the last-applied manifest", domain.PodQuery{Limit: 50, Columns: []domain.CustomColumn{{Source: "annotation", Key: "kubectl.kubernetes.io/last-applied-configuration"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewPodQuery(tt.query)
			if (err == nil) != tt.ok {
				t.Fatalf("NewPodQuery() error = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, domain.ErrInvalidPodQuery) {
				t.Fatalf("error %v is not ErrInvalidPodQuery", err)
			}
		})
	}
}

func TestQueryPodsPagesAndCounts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	pods := synthPods(t, 120)

	tests := []struct {
		name       string
		query      domain.PodQuery
		wantOffset int
		wantRows   int
		wantFirst  string
	}{
		{"first page", domain.PodQuery{Limit: 50}, 0, 50, "pod-000"},
		{"last page is short", domain.PodQuery{Limit: 50, Offset: 100}, 100, 20, "pod-100"},
		{"past the end lands on the last page", domain.PodQuery{Limit: 50, Offset: 900}, 100, 20, "pod-100"},
		{"an offset inside a page starts the page", domain.PodQuery{Limit: 50, Offset: 60}, 50, 50, "pod-050"},
		{"sorted descending by name", domain.PodQuery{Limit: 10, SortColumn: "name", Descending: true}, 0, 10, "pod-119"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			page := domain.QueryPods(pods, tt.query, now, collation.Weight)
			if page.Offset != tt.wantOffset || len(page.Rows) != tt.wantRows {
				t.Fatalf("offset %d rows %d, want %d and %d", page.Offset, len(page.Rows), tt.wantOffset, tt.wantRows)
			}
			if got := page.Rows[0].Name(); got != tt.wantFirst {
				t.Fatalf("first row %q, want %q", got, tt.wantFirst)
			}
			if page.Matched != 120 || page.Total != 120 {
				t.Fatalf("matched %d total %d, want 120 and 120", page.Matched, page.Total)
			}
		})
	}

	t.Run("an empty match is one empty page", func(t *testing.T) {
		t.Parallel()
		page := domain.QueryPods(pods, domain.PodQuery{Text: "nothing-is-called-this", Limit: 50, Offset: 150}, now, collation.Weight)
		if page.Matched != 0 || len(page.Rows) != 0 || page.Offset != 0 {
			t.Fatalf("got %+v, want an empty first page", page)
		}
	})

	t.Run("an invalid pattern says so and matches nothing", func(t *testing.T) {
		t.Parallel()
		page := domain.QueryPods(pods, domain.PodQuery{Text: "re:(", Limit: 50}, now, collation.Weight)
		if page.Matched != 0 || page.QueryError == "" {
			t.Fatalf("got matched %d error %q, want none and an explanation", page.Matched, page.QueryError)
		}
	})
}

func TestMatchingPodsIgnoresThePage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	pods := synthPods(t, 120)
	matched := domain.MatchingPods(pods, domain.PodQuery{Text: "pod-1", Limit: 1, SortColumn: "name", Descending: true}, now, collation.Weight)

	// pod-1, pod-10..pod-19 do not exist (names are zero-padded); pod-100
	// to pod-119 do.
	if len(matched) != 20 || matched[0].Name() != "pod-119" {
		t.Fatalf("got %d rows starting %q, want 20 starting pod-119", len(matched), matched[0].Name())
	}
}

// TestDisplayedMemoryTiesKeepListOrder pins the one place the sort reads
// what is DISPLAYED rather than what was measured: two pods whose memory
// both reads 256.0MiB are a tie, left in list order.
func TestDisplayedMemoryTiesKeepListOrder(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	first := mustPod(t, "a-larger", domain.NewMetrics(1, 256<<20+40_000))
	second := mustPod(t, "b-smaller", domain.NewMetrics(1, 256<<20))

	page := domain.QueryPods([]domain.Pod{first, second}, domain.PodQuery{Limit: 10, SortColumn: "memory"}, now, collation.Weight)
	if page.Rows[0].Name() != "a-larger" {
		t.Fatalf("got %q first: the sort read bytes the table does not show", page.Rows[0].Name())
	}
}

func mustPod(t testing.TB, name string, usage domain.Metrics) domain.Pod {
	t.Helper()
	pod, err := domain.NewPod(domain.PodSpec{Name: name, Namespace: "default", ClusterID: "test", Phase: domain.PodPhaseRunning, Usage: usage})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	return pod
}
