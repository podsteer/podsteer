package wails

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// PodQuery is one page query of the pod table — what the frontend's search
// box, status chips, sort and pager currently say. See domain.PodQuery.
type PodQuery struct {
	// Text is the search box, in the filter language of web/src/lib/query.ts.
	Text string `json:"text"`
	// Chips are the pressed status chips' ids.
	Chips []string `json:"chips"`
	// SortColumn is the sorted column's id, "" for the list's own order.
	SortColumn string `json:"sortColumn"`
	// Descending reverses the sort.
	Descending bool `json:"descending"`
	// Columns are the operator's own columns on the list — searchable text,
	// and a sort can name one.
	Columns []CustomColumnSpec `json:"columns"`
	// Clusters narrows the merged All-clusters list; empty means every
	// cluster. Ignored on one cluster's list.
	Clusters []string `json:"clusters"`
	// Offset and Limit select the page.
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// CustomColumnSpec is one custom column, as $lib/customColumns persists it.
type CustomColumnSpec struct {
	// Source is "label", "annotation" or "jsonpath".
	Source string `json:"source"`
	// Key is the label or annotation key, or the JSONPath expression.
	Key string `json:"key"`
}

func (q PodQuery) toDomain() domain.PodQuery {
	query := domain.PodQuery{
		Text:       q.Text,
		SortColumn: q.SortColumn,
		Descending: q.Descending,
		Clusters:   q.Clusters,
		Offset:     q.Offset,
		Limit:      q.Limit,
	}
	for _, chip := range q.Chips {
		query.Chips = append(query.Chips, domain.PodStatusChip(chip))
	}
	for _, column := range q.Columns {
		query.Columns = append(query.Columns, domain.CustomColumn{Source: domain.CustomColumnSource(column.Source), Key: column.Key})
	}
	return query
}

// PodPage is one page of the pod table and the counts around it.
type PodPage struct {
	// Rows are the page, in display order.
	Rows []Pod `json:"rows"`
	// Offset is where the page starts, after clamping past-the-end offsets
	// to the last page.
	Offset int `json:"offset"`
	// Matched counts what the search and chips kept: the pager's total.
	Matched int `json:"matched"`
	// Total counts the list before any filter.
	Total int `json:"total"`
	// Unhealthy counts the unhealthy pods before any filter.
	Unhealthy int `json:"unhealthy"`
	// ChipCounts holds, per chip id, how many of the SEARCHED rows it would
	// select.
	ChipCounts map[string]int `json:"chipCounts"`
	// QueryError explains a search that did not parse, and so matched
	// nothing — a regex the webview accepts and Go's dialect does not.
	QueryError string `json:"queryError"`
}

func toPodPage(page domain.PodPage, now time.Time) PodPage {
	counts := make(map[string]int, len(page.ChipCounts))
	for chip, count := range page.ChipCounts {
		counts[string(chip)] = count
	}
	return PodPage{
		Rows:       toPods(page.Rows, now),
		Offset:     page.Offset,
		Matched:    page.Matched,
		Total:      page.Total,
		Unhealthy:  page.Unhealthy,
		ChipCounts: counts,
		QueryError: page.QueryError,
	}
}

// PodKey names one pod for "select all matching", with the controller a
// bulk plan reads off a row.
type PodKey struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	UID          string `json:"uid"`
	ControlledBy string `json:"controlledBy"`
	Cluster      string `json:"cluster"`
}

func toPodKeys(keys []domain.PodKey) []PodKey {
	out := make([]PodKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, PodKey{
			Namespace:    key.Namespace,
			Name:         key.Name,
			UID:          key.UID,
			ControlledBy: key.ControlledBy,
			Cluster:      key.Cluster,
		})
	}
	return out
}

// CSVColumn is one column of a CSV export: the table's column id, which
// decides the cell, and the heading it is shown under.
type CSVColumn struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// podCSVCell is the text a pod table cell shows, for the export — the
// status word rather than the phase, the quantity with its unit, the age
// already coarsened. The same switch PodsView and FleetView rendered their
// exports with before the rows stopped being in the webview.
func podCSVCell(pod Pod, id string) string {
	if source, key, ok := strings.Cut(id, ":"); ok && source != "" {
		var value string
		switch source {
		case "label":
			value = pod.Labels[key]
		case "annotation":
			value = pod.Annotations[key]
		case "jsonpath":
			value = pod.Custom[id]
		}
		if value == "" {
			return "—"
		}
		return value
	}

	switch id {
	case "status":
		if pod.StatusReason != "" {
			return pod.StatusReason
		}
		return pod.Phase
	case "cluster":
		return pod.ClusterID
	case "name":
		return pod.Name
	case "namespace":
		return pod.Namespace
	case "cpu":
		return pod.CPU
	case "memory":
		return pod.Memory
	case "ready":
		return pod.Ready
	case "restarts":
		return fmt.Sprint(pod.Restarts)
	case "controlledBy":
		return dashIfEmpty(pod.ControlledBy)
	case "node":
		return dashIfEmpty(pod.NodeName)
	case "qos":
		return dashIfEmpty(pod.QoSClass)
	case "ip":
		return dashIfEmpty(pod.PodIP)
	case "age":
		return formatAge(float64(pod.AgeSeconds))
	default:
		return ""
	}
}

func dashIfEmpty(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// formatAge is web/src/lib/format.ts formatAge, for the export's age cells:
// two units while the larger is small, one once the smaller stops mattering.
func formatAge(seconds float64) string {
	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
		year   = 365 * day
	)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return "—"
	}
	whole := int64(seconds)
	switch {
	case seconds < minute:
		return fmt.Sprintf("%ds", whole)
	case seconds < hour:
		minutes, rest := whole/minute, whole%minute
		if rest > 0 && minutes < 10 {
			return fmt.Sprintf("%dm%ds", minutes, rest)
		}
		return fmt.Sprintf("%dm", minutes)
	case seconds < day:
		hours, minutes := whole/hour, whole%hour/minute
		if minutes > 0 {
			return fmt.Sprintf("%dh%dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case seconds < year:
		days, hours := whole/day, whole%day/hour
		if days < 10 && hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	default:
		years, days := whole/year, whole%year/day
		if days > 0 {
			return fmt.Sprintf("%dy%dd", years, days)
		}
		return fmt.Sprintf("%dy", years)
	}
}

// renderPodCSV is the export file: web/src/lib/csv.ts toCSV over the pod
// cells — RFC 4180 quoting, CRLF after every line including the last, UTF-8
// with no byte-order mark, and the formula guard on every field, heading
// included.
func renderPodCSV(columns []CSVColumn, pods []Pod) string {
	var out strings.Builder
	writeLine := func(fields []string) {
		for i, field := range fields {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteCSVField(neutraliseFormula(field)))
		}
		out.WriteString("\r\n")
	}

	headings := make([]string, len(columns))
	for i, column := range columns {
		headings[i] = column.Label
	}
	writeLine(headings)

	cells := make([]string, len(columns))
	for _, pod := range pods {
		for i, column := range columns {
			cells[i] = podCSVCell(pod, column.ID)
		}
		writeLine(cells)
	}
	return out.String()
}

// neutraliseFormula prefixes an apostrophe to a field a spreadsheet would
// run as a formula. Every field comes from the cluster, so every field is
// untrusted input to whatever opens the file.
func neutraliseFormula(field string) string {
	if field == "" {
		return field
	}
	switch field[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + field
	}
	return field
}

func quoteCSVField(field string) string {
	if !strings.ContainsAny(field, "\",\r\n") {
		return field
	}
	return `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
}
