package domain

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The pod table's search, status chips, sort and page, answered in Go.
//
// WHY HERE AND NOT IN THE WEBVIEW. Every row carries live usage, so the whole
// list changes on every tick, and it used to cross the bridge whole on every
// tick to be filtered and sliced to fifty rows in the webview: 11 MB at five
// thousand pods, 23 MB at ten (TestPodListPayloadSize). The read cache
// already holds the mapped list for the tick, so filtering it here is CPU
// over memory, and the bridge carries one page and the counts that page
// needs.
//
// THE RULES ARE THE FRONTEND'S, ported rather than redesigned: the text
// language is $lib/query (see TextQuery), the chips $lib/podStatusFilters,
// the collation $lib/sort (see textorder.go), the custom columns'
// contribution $lib/customColumns. web/src/lib/filter.fixtures.json holds
// the two implementations together; podquery_test.go and filter.node.test.ts both
// run it.

// PodStatusChip names one of the pod table's status quick-filters.
type PodStatusChip string

// The chips, in the order the table draws them. Each one SELECTS on a value
// the domain already computed — IsHealthy, the phase, StatusReason, a
// container's last termination — and decides nothing new.
const (
	PodChipFailing          PodStatusChip = "failing"
	PodChipPending          PodStatusChip = "pending"
	PodChipRestarting       PodStatusChip = "restarting"
	PodChipOOMKilled        PodStatusChip = "oomkilled"
	PodChipImagePullBackOff PodStatusChip = "imagepullbackoff"
	PodChipTerminating      PodStatusChip = "terminating"
)

// PodStatusChips returns every chip, in display order.
func PodStatusChips() []PodStatusChip {
	return []PodStatusChip{
		PodChipFailing,
		PodChipPending,
		PodChipRestarting,
		PodChipOOMKilled,
		PodChipImagePullBackOff,
		PodChipTerminating,
	}
}

// selects reports whether row belongs in the chip.
func (c PodStatusChip) selects(row *PodQueryRow) bool {
	switch c {
	case PodChipFailing:
		// Not phase Failed: a crash-looping pod reports Running while serving
		// nothing. Pending and Terminating have chips of their own.
		return !row.IsHealthy && row.Phase != string(PodPhasePending) && row.Phase != string(PodPhaseTerminating)
	case PodChipPending:
		return row.Phase == string(PodPhasePending)
	case PodChipRestarting:
		return row.StatusReason == "CrashLoopBackOff"
	case PodChipOOMKilled:
		// The current state, or a previous life that ended in the kill —
		// StatusReason reports only the current one.
		return row.StatusReason == "OOMKilled" || slices.Contains(row.LastTerminationReasons, "OOMKilled")
	case PodChipImagePullBackOff:
		return row.StatusReason == "ImagePullBackOff"
	case PodChipTerminating:
		return row.Phase == string(PodPhaseTerminating)
	default:
		return false
	}
}

// CustomColumnSource is where an operator's own column reads its value.
type CustomColumnSource string

// The three sources, as $lib/customColumns names them.
const (
	CustomColumnLabel      CustomColumnSource = "label"
	CustomColumnAnnotation CustomColumnSource = "annotation"
	CustomColumnJSONPath   CustomColumnSource = "jsonpath"
)

// CustomColumn is one of the operator's own columns on the list: a source
// and a key (for a JSONPath column, the expression).
//
// It travels with the query because a custom column's value is SEARCHABLE —
// a value visible on screen must be findable from the box above it — and
// because a sort can name one.
type CustomColumn struct {
	Source CustomColumnSource
	Key    string
}

// ID is the column's id, "source:key", as the table knows it.
func (c CustomColumn) ID() string { return string(c.Source) + ":" + c.Key }

// lastAppliedAnnotation can never be a column — see NewProjection.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// valid is $lib/customColumns isValidKey.
func (c CustomColumn) valid() bool {
	if c.Key == "" || strings.ContainsFunc(c.Key, func(r rune) bool { return isQuerySpace(r) || r == ',' }) {
		return false
	}
	switch c.Source {
	case CustomColumnJSONPath:
		return strings.HasPrefix(c.Key, ".") || strings.HasPrefix(c.Key, "{")
	case CustomColumnAnnotation:
		return c.Key != lastAppliedAnnotation
	case CustomColumnLabel:
		return true
	default:
		return false
	}
}

// parseCustomColumnID reads a sort column id back into a column, split on
// the first colon. False for a built-in column id.
func parseCustomColumnID(id string) (CustomColumn, bool) {
	at := strings.IndexByte(id, ':')
	if at <= 0 {
		return CustomColumn{}, false
	}
	column := CustomColumn{Source: CustomColumnSource(id[:at]), Key: id[at+1:]}
	return column, column.valid()
}

// value is what a row shows under the column, "" when it has none.
func (c CustomColumn) value(row *PodQueryRow) string {
	switch c.Source {
	case CustomColumnJSONPath:
		// Rendered in Go and filed under the column's id, not its path.
		return row.Custom[c.ID()]
	case CustomColumnLabel:
		return row.Labels[c.Key]
	default:
		return row.Annotations[c.Key]
	}
}

// MaxPodPageSize is the most rows one page may ask for. The table offers 25
// to 200; the cap is what keeps a "page" from quietly becoming the whole list
// again, which is the payload this exists to avoid. Whole-list reads — the
// keys behind "select all matching", the CSV export — go through
// MatchingPods instead, deliberately and on a gesture.
const MaxPodPageSize = 500

// ErrInvalidPodQuery is returned for a query that cannot be answered as
// asked.
var ErrInvalidPodQuery = errors.New("invalid pod query")

// PodQuery is one question about a pod list: what to keep, in what order,
// and which slice of it to return.
type PodQuery struct {
	// Text is the search box, in the TextQuery language.
	Text string
	// Chips are the pressed status chips. OR together; ANDed with Text.
	// An unknown id selects nothing, as in the webview.
	Chips []PodStatusChip
	// SortColumn is the table's column id — a built-in one, or a custom
	// column's "source:key" — or "" for the list's own order. An id the
	// table does not know leaves the order alone.
	SortColumn string
	// Descending reverses the sort. Unmeasured values sort last either way.
	Descending bool
	// Columns are the operator's own columns on this list, in order.
	Columns []CustomColumn
	// Clusters narrows a merged list to these clusters, applied BEFORE the
	// search: several clusters are an OR and the query language ANDs. Empty
	// means every cluster. Ignored on a single cluster's list.
	Clusters []string
	// Offset and Limit select the page. Offset past the end lands on the
	// last page, as the table's own pager clamps it.
	Offset int
	Limit  int
}

// NewPodQuery validates a page query.
func NewPodQuery(q PodQuery) (PodQuery, error) {
	switch {
	case q.Limit < 1 || q.Limit > MaxPodPageSize:
		return PodQuery{}, fmt.Errorf("%w: page size %d is outside 1-%d", ErrInvalidPodQuery, q.Limit, MaxPodPageSize)
	case q.Offset < 0:
		return PodQuery{}, fmt.Errorf("%w: negative offset %d", ErrInvalidPodQuery, q.Offset)
	}
	for _, column := range q.Columns {
		if !column.valid() {
			return PodQuery{}, fmt.Errorf("%w: custom column %q", ErrInvalidPodQuery, column.ID())
		}
	}
	return q, nil
}

// PodQueryRow is one pod as the query reads it — exactly the values the
// table shows and sorts by, and nothing else.
//
// A flat value rather than the Pod itself because two of the sort keys are
// what the table DISPLAYS rather than what was measured: memory is shown to
// one decimal place, and two pods that read "256.0MiB" are a tie the table's
// stable sort leaves in list order. Sorting on the raw bytes would order
// rows the operator sees as equal.
type PodQueryRow struct {
	Name, Namespace, NodeName, Phase, StatusReason string
	ControlledBy, QoSClass, PodIP, Cluster         string

	IsHealthy              bool
	LastTerminationReasons []string

	ReadyContainers int
	Restarts        int32
	AgeSeconds      int64

	// CPU is cores and Memory bytes, as displayed; nil when unmeasured,
	// which sorts last. NewPodQueryRow leaves both nil and keeps the
	// measurement instead, read only if the table sorts by it.
	CPU, Memory *float64

	usage Metrics

	Labels, Annotations, Custom map[string]string
}

// NewPodQueryRow reads pod as the table shows it at now.
func NewPodQueryRow(pod Pod, now time.Time) PodQueryRow {
	row := PodQueryRow{
		Name:            pod.name,
		Namespace:       pod.namespace.String(),
		NodeName:        pod.nodeName,
		Phase:           string(pod.phase),
		StatusReason:    pod.StatusReason(),
		QoSClass:        string(pod.qosClass),
		PodIP:           pod.podIP,
		Cluster:         string(pod.clusterID),
		IsHealthy:       pod.IsHealthy(),
		ReadyContainers: pod.ReadyContainers(),
		Restarts:        pod.RestartCount(),
		AgeSeconds:      int64(pod.Age(now).Seconds()),
		Labels:          pod.labels,
		Annotations:     pod.annotations,
		Custom:          pod.custom,
	}
	if owner := pod.Controller(); !owner.IsZero() {
		row.ControlledBy = owner.Kind + "/" + owner.Name
	}
	for _, container := range pod.containers {
		if !container.LastTermination.IsZero() {
			row.LastTerminationReasons = append(row.LastTerminationReasons, container.LastTermination.Reason)
		}
	}
	row.usage = pod.usage
	return row
}

// cpu is the row's CPU as displayed, in cores, or nil when unmeasured.
func (r *PodQueryRow) cpu() *float64 {
	if r.CPU != nil || !r.usage.Measured {
		return r.CPU
	}
	cores := float64(r.usage.CPUMilli) / 1000
	return &cores
}

// memory is the row's memory as displayed, in bytes, or nil for the dash.
func (r *PodQueryRow) memory() *float64 {
	if r.Memory != nil || !r.usage.Measured {
		return r.Memory
	}
	return DisplayedBytes(r.usage.MemoryBytes)
}

// binaryUnitMultipliers are the units memory is displayed in, B to PiB.
var binaryUnitMultipliers = [...]float64{1, 1 << 10, 1 << 20, 1 << 30, 1 << 40, 1 << 50}

// DisplayedBytes is a byte count as the table displays it — one decimal
// place in the largest binary unit it reaches — read back as a number, or
// nil for the dash a non-positive count is shown as. It is the frontend's
// parseQuantity of the wails adapter's formatBytes; the wails package holds
// a test that the two agree.
func DisplayedBytes(bytes int64) *float64 {
	if bytes <= 0 {
		return nil
	}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(binaryUnitMultipliers)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		shown := float64(bytes)
		return &shown
	}
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 1, 64), 64)
	if err != nil {
		return nil
	}
	shown := rounded * binaryUnitMultipliers[unit]
	return &shown
}

// PodKey identifies one pod for a bulk action, with the one fact a bulk
// plan reads off a pod row: its controller. "Select all matching" fetches
// these for every match rather than every match's full row — a fraction of
// the payload, on a gesture rather than a tick.
type PodKey struct {
	Namespace    string
	Name         string
	UID          string
	ControlledBy string
	Cluster      string
}

// PodPage is one page of a pod list and what the table around it needs.
type PodPage struct {
	// Rows are the page, in display order.
	Rows []Pod
	// Offset is where the page starts, after clamping.
	Offset int
	// Matched counts the rows the search and the chips kept — what the
	// pager and the row count read.
	Matched int
	// Total counts every row before any filter.
	Total int
	// Unhealthy counts the rows, before any filter, that are not healthy —
	// the header's "N unhealthy".
	Unhealthy int
	// ChipCounts counts, per chip, the rows the SEARCH kept that the chip
	// would select — against the searched rows rather than the chipped
	// ones, so an unpressed chip still says what pressing it would add.
	ChipCounts map[PodStatusChip]int
	// QueryError explains a search that could not be parsed, and so matched
	// nothing. Empty otherwise.
	QueryError string
}

// podQueryResult is a query's verdict over a set of rows, as indices into
// them, before any page is cut.
type podQueryResult struct {
	order      []int
	chipCounts map[PodStatusChip]int
	queryErr   error
}

// runPodQuery filters, counts and sorts rows. Pure, and the one place the
// rules live: QueryPods, MatchingPods and the merged All-clusters list all
// come through here.
func runPodQuery(rows []PodQueryRow, q PodQuery, weight RuneWeight) podQueryResult {
	text := ParseTextQuery(q.Text)
	result := podQueryResult{chipCounts: make(map[PodStatusChip]int, len(PodStatusChips())), queryErr: text.Err()}
	for _, chip := range PodStatusChips() {
		result.chipCounts[chip] = 0
	}

	searched := make([]int, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if len(q.Clusters) > 0 && !slices.Contains(q.Clusters, row.Cluster) {
			continue
		}
		if !text.IsEmpty() && !text.matches(searchable(row, q.Columns)) {
			continue
		}
		searched = append(searched, i)
	}

	for _, i := range searched {
		for _, chip := range PodStatusChips() {
			if chip.selects(&rows[i]) {
				result.chipCounts[chip]++
			}
		}
	}

	result.order = searched
	if len(q.Chips) > 0 {
		result.order = make([]int, 0, len(searched))
		for _, i := range searched {
			if selectedByChips(&rows[i], q.Chips) {
				result.order = append(result.order, i)
			}
		}
	}

	sortPodRows(rows, result.order, q, weight)
	return result
}

// selectedByChips is an OR across the pressed chips, in display order.
func selectedByChips(row *PodQueryRow, pressed []PodStatusChip) bool {
	for _, chip := range PodStatusChips() {
		if slices.Contains(pressed, chip) && chip.selects(row) {
			return true
		}
	}
	return false
}

// searchable is the row as the text query reads it: name, namespace, node
// and phase, then each custom column's value, empty fields dropped, joined
// by single spaces — the one string a substring or a regex runs over.
func searchable(row *PodQueryRow, columns []CustomColumn) queryRow {
	fields := make([]string, 0, 4+len(columns))
	for _, field := range []string{row.Name, row.Namespace, row.NodeName, row.Phase} {
		if field != "" {
			fields = append(fields, field)
		}
	}
	for _, column := range columns {
		if value := column.value(row); value != "" {
			fields = append(fields, value)
		}
	}
	text := strings.Join(fields, " ")
	return queryRow{text: text, lowered: strings.ToLower(text), labels: row.Labels, cluster: row.Cluster}
}

// sortValue is one cell as the sort compares it: a number, text, or
// nothing — which sorts last in both directions.
type sortValue struct {
	null    bool
	numeric bool
	number  float64
	text    string
}

func textValue(text string) sortValue { return sortValue{text: text} }
func numberValue(n float64) sortValue { return sortValue{numeric: true, number: n} }
func optionalNumber(n *float64) sortValue {
	if n == nil {
		return sortValue{null: true}
	}
	return numberValue(*n)
}

// podSortAccessors are the table's columns, by id — the session's POD_SORT,
// plus the cluster column the merged list adds. On one cluster's list every
// row has the same cluster, so sorting by it is the stable no-op the webview
// produced by not knowing the column.
var podSortAccessors = map[string]func(row *PodQueryRow) sortValue{
	"status": func(row *PodQueryRow) sortValue {
		if row.StatusReason != "" {
			return textValue(row.StatusReason)
		}
		return textValue(row.Phase)
	},
	"name":         func(row *PodQueryRow) sortValue { return textValue(row.Name) },
	"namespace":    func(row *PodQueryRow) sortValue { return textValue(row.Namespace) },
	"cpu":          func(row *PodQueryRow) sortValue { return optionalNumber(row.cpu()) },
	"memory":       func(row *PodQueryRow) sortValue { return optionalNumber(row.memory()) },
	"ready":        func(row *PodQueryRow) sortValue { return numberValue(float64(row.ReadyContainers)) },
	"restarts":     func(row *PodQueryRow) sortValue { return numberValue(float64(row.Restarts)) },
	"controlledBy": func(row *PodQueryRow) sortValue { return textValue(row.ControlledBy) },
	"node":         func(row *PodQueryRow) sortValue { return textValue(row.NodeName) },
	"qos":          func(row *PodQueryRow) sortValue { return textValue(row.QoSClass) },
	"ip":           func(row *PodQueryRow) sortValue { return textValue(row.PodIP) },
	"age":          func(row *PodQueryRow) sortValue { return numberValue(float64(row.AgeSeconds)) },
	"cluster":      func(row *PodQueryRow) sortValue { return textValue(row.Cluster) },
}

// sortAccessor resolves a column id: a built-in column, a custom column
// (whose empty value is "nothing", not ""), or nil for an id this table does
// not know, which leaves the order alone.
func sortAccessor(columnID string) func(row *PodQueryRow) sortValue {
	if accessor, ok := podSortAccessors[columnID]; ok {
		return accessor
	}
	if column, ok := parseCustomColumnID(columnID); ok {
		return func(row *PodQueryRow) sortValue {
			if value := column.value(row); value != "" {
				return textValue(value)
			}
			return sortValue{null: true}
		}
	}
	return nil
}

// sortPodRows orders the indices in place by the query's column.
//
// STABLE, and each value is read once: ties keep the list's own order, as
// Array.prototype.sort does, and a text value is prepared for collation once
// per row rather than once per comparison.
//
// Unmeasured values go last in both directions, which with a stable sort is
// exactly "the sorted values, then the unmeasured ones in list order" — so
// they are set aside first and the sort itself compares only like with like:
// numbers as numbers, text by collation key. Every column is one or the
// other; a column that mixed the two would compare a number as its text, as
// String(value) does in the webview.
func sortPodRows(rows []PodQueryRow, order []int, q PodQuery, weight RuneWeight) {
	accessor := sortAccessor(q.SortColumn)
	if accessor == nil || len(order) < 2 {
		return
	}
	if weight == nil {
		// Every caller wires the real order (app/adapters/collation); this
		// only keeps a missing one from being a nil call. Code point order
		// is still a total order — wrong for case and accents, never a panic.
		weight = func(r rune) uint64 { return uint64(r) + 1 }
	}

	sign := 1
	if q.Descending {
		sign = -1
	}

	type numberEntry struct {
		index  int
		number float64
	}
	type textEntry struct {
		index int
		key   textKey
	}
	var (
		numbers []numberEntry
		texts   []textEntry
		nulls   []int
	)
	for _, index := range order {
		value := accessor(&rows[index])
		switch {
		case value.null:
			nulls = append(nulls, index)
		case value.numeric:
			numbers = append(numbers, numberEntry{index: index, number: value.number})
		default:
			texts = append(texts, textEntry{index: index, key: newTextKey(value.text, weight)})
		}
	}

	if len(numbers) > 0 && len(texts) > 0 {
		// A mixed column: every number as its text, back in list order so
		// the stable sort still keeps ties where the list had them.
		position := make(map[int]int, len(order))
		for i, index := range order {
			position[index] = i
		}
		for _, entry := range numbers {
			texts = append(texts, textEntry{index: entry.index, key: newTextKey(strconv.FormatFloat(entry.number, 'f', -1, 64), weight)})
		}
		numbers = nil
		slices.SortFunc(texts, func(a, b textEntry) int { return cmp.Compare(position[a.index], position[b.index]) })
	}

	sorted := order[:0]
	if len(numbers) > 0 {
		slices.SortStableFunc(numbers, func(a, b numberEntry) int { return sign * cmp.Compare(a.number, b.number) })
		for _, entry := range numbers {
			sorted = append(sorted, entry.index)
		}
	} else {
		digit := weight('0')
		slices.SortStableFunc(texts, func(a, b textEntry) int { return sign * compareTextKeys(a.key, b.key, digit) })
		for _, entry := range texts {
			sorted = append(sorted, entry.index)
		}
	}
	// In place: the sorted values then the unmeasured ones fill order
	// exactly, since every index went to one or the other.
	copy(order[len(sorted):], nulls)
}

// pageBounds clamps a page to what exists: an offset past the end lands on
// the last page, as the table's pager does.
func pageBounds(matched, offset, limit int) (start, end int) {
	if limit < 1 {
		limit = 1
	}
	pages := max(1, int(math.Ceil(float64(matched)/float64(limit))))
	page := min(offset/limit, pages-1)
	start = page * limit
	return start, min(start+limit, matched)
}

// QueryPods answers a page query over a list, in the list's own order when
// nothing is sorted. weight orders text; see RuneWeight.
func QueryPods(pods []Pod, q PodQuery, now time.Time, weight RuneWeight) PodPage {
	rows := make([]PodQueryRow, len(pods))
	for i := range pods {
		rows[i] = NewPodQueryRow(pods[i], now)
	}
	result := runPodQuery(rows, q, weight)

	page := PodPage{
		Matched:    len(result.order),
		Total:      len(pods),
		ChipCounts: result.chipCounts,
	}
	if result.queryErr != nil {
		page.QueryError = result.queryErr.Error()
	}
	for i := range rows {
		if !rows[i].IsHealthy {
			page.Unhealthy++
		}
	}

	start, end := pageBounds(len(result.order), q.Offset, q.Limit)
	page.Offset = start
	page.Rows = make([]Pod, 0, end-start)
	for _, index := range result.order[start:end] {
		page.Rows = append(page.Rows, pods[index])
	}
	return page
}

// MatchingPods is every pod the query keeps, in display order, ignoring the
// page — for the reads that are about the whole match on purpose: the keys
// behind "select all matching" and the CSV export.
func MatchingPods(pods []Pod, q PodQuery, now time.Time, weight RuneWeight) []Pod {
	rows := make([]PodQueryRow, len(pods))
	for i := range pods {
		rows[i] = NewPodQueryRow(pods[i], now)
	}
	result := runPodQuery(rows, q, weight)

	matched := make([]Pod, 0, len(result.order))
	for _, index := range result.order {
		matched = append(matched, pods[index])
	}
	return matched
}

// KeyOf is a pod's PodKey.
func KeyOf(pod Pod) PodKey {
	key := PodKey{
		Namespace: pod.namespace.String(),
		Name:      pod.name,
		UID:       pod.uid,
		Cluster:   string(pod.clusterID),
	}
	if owner := pod.Controller(); !owner.IsZero() {
		key.ControlledBy = owner.Kind + "/" + owner.Name
	}
	return key
}
