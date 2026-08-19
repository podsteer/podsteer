package wails

import (
	"fmt"
	"time"

	"k8sense/app/domain"
)

// Wire types for the resources the navigator browses. As with dto.go these are
// the frontend API contract — Wails generates TypeScript from them — so a
// field rename is a breaking change.

// ResourceKind is a browsable kind, as presented to the navigator.
type ResourceKind struct {
	// ID is the handle every list call takes back, "group/version/resource".
	ID string `json:"id"`
	// Group is the API group, empty for the core group.
	Group string `json:"group"`
	// Version is the API version.
	Version string `json:"version"`
	// Kind is the CamelCase singular, e.g. "Deployment".
	Kind string `json:"kind"`
	// Namespaced reports whether objects live in a namespace. The UI hides
	// the namespace filter and column when false.
	Namespaced bool `json:"namespaced"`
	// Category places the kind in a navigator section.
	Category string `json:"category"`
	// Title is the plural display name.
	Title string `json:"title"`
	// Singular is the singular display name.
	Singular string `json:"singular"`
	// Rich reports whether K8Sense has purpose-built columns for this kind.
	// The UI routes rich kinds to their own view and everything else to the
	// generic table.
	Rich bool `json:"rich"`
}

func toResourceKind(kind domain.ResourceKind) ResourceKind {
	return ResourceKind{
		ID:         kind.ID(),
		Group:      kind.Group,
		Version:    kind.Version,
		Kind:       kind.Kind,
		Namespaced: kind.Namespaced,
		Category:   string(kind.Category),
		Title:      kind.Title,
		Singular:   kind.Singular,
		Rich:       kind.Rich,
	}
}

func toResourceKinds(kinds []domain.ResourceKind) []ResourceKind {
	out := make([]ResourceKind, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, toResourceKind(kind))
	}
	return out
}

// Node is a cluster node as presented to the UI.
type Node struct {
	Name string `json:"name"`
	// Status is the single word to show: Ready, NotReady, SchedulingDisabled
	// or the name of whichever pressure condition is firing.
	Status string `json:"status"`
	// Roles are the node's roles, e.g. ["control-plane"].
	Roles []string `json:"roles"`
	// IsControlPlane lets the UI mark control-plane nodes without re-parsing
	// the roles.
	IsControlPlane bool `json:"isControlPlane"`
	IsHealthy      bool `json:"isHealthy"`
	Unschedulable  bool `json:"unschedulable"`
	Taints         int  `json:"taints"`
	// CPU is usage in cores, formatted as the pod table shows it.
	CPU string `json:"cpu"`
	// CPUPercent is usage against allocatable, for the meter.
	CPUPercent float64 `json:"cpuPercent"`
	Memory     string  `json:"memory"`
	// MemoryPercent is usage against allocatable.
	MemoryPercent float64 `json:"memoryPercent"`
	// HasMetrics distinguishes "measured zero" from "no metrics-server", so
	// the UI can show a dash rather than a misleading 0%.
	HasMetrics     bool   `json:"hasMetrics"`
	Version        string `json:"version"`
	OSImage        string `json:"osImage"`
	Architecture   string `json:"architecture"`
	InternalIP     string `json:"internalIp"`
	AllocatableCPU string `json:"allocatableCpu"`
	AllocatableMem string `json:"allocatableMemory"`
	MaxPods        int64  `json:"maxPods"`
	CreatedAt      string `json:"createdAt"`
	AgeSeconds     int64  `json:"ageSeconds"`
}

func toNode(node domain.Node, now time.Time) Node {
	usage := node.Usage()
	allocatable := node.Allocatable()

	return Node{
		Name:           node.Name(),
		Status:         node.Status(),
		Roles:          node.Roles(),
		IsControlPlane: node.IsControlPlane(),
		IsHealthy:      node.IsHealthy(),
		Unschedulable:  node.Unschedulable(),
		Taints:         node.Taints(),
		CPU:            formatCores(usage),
		CPUPercent:     node.CPUPercent(),
		Memory:         formatMemory(usage),
		MemoryPercent:  node.MemoryPercent(),
		HasMetrics:     !usage.IsZero(),
		Version:        node.KubeletVersion(),
		OSImage:        node.OSImage(),
		Architecture:   node.Architecture(),
		InternalIP:     node.InternalIP(),
		AllocatableCPU: formatMilliCores(allocatable.CPUMilli),
		AllocatableMem: formatBytes(allocatable.MemoryBytes),
		MaxPods:        allocatable.Pods,
		CreatedAt:      formatTime(node.CreatedAt()),
		AgeSeconds:     int64(node.Age(now).Seconds()),
	}
}

func toNodes(nodes []domain.Node, now time.Time) []Node {
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, toNode(node, now))
	}
	return out
}

// Workload is a pod-managing controller as presented to the UI.
type Workload struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Status is the single word to show: Running, Degraded, Rolling,
	// Unavailable, Suspended or "Scaled to zero".
	Status string `json:"status"`
	// Ready is the ready/desired count formatted for display, "2/3".
	Ready      string `json:"ready"`
	Desired    int32  `json:"desired"`
	ReadyCount int32  `json:"readyCount"`
	Current    int32  `json:"current"`
	Updated    int32  `json:"updated"`
	Available  int32  `json:"available"`
	IsHealthy  bool   `json:"isHealthy"`
	IsRolling  bool   `json:"isRolling"`
	Suspended  bool   `json:"suspended"`
	// Images are the pod template's container images.
	Images []string `json:"images"`
	// ControlledBy names the controlling owner, e.g. a Deployment behind a
	// ReplicaSet. Empty when nothing controls it.
	ControlledBy string `json:"controlledBy"`
	// Schedule is a CronJob's cron expression.
	Schedule      string            `json:"schedule"`
	LastScheduled string            `json:"lastScheduled"`
	Labels        map[string]string `json:"labels"`
	CreatedAt     string            `json:"createdAt"`
	AgeSeconds    int64             `json:"ageSeconds"`
}

func toWorkload(workload domain.Workload, now time.Time) Workload {
	labels := workload.Labels()
	if labels == nil {
		labels = map[string]string{}
	}

	images := workload.Images()
	if images == nil {
		images = []string{}
	}

	return Workload{
		Kind:          string(workload.Kind()),
		Name:          workload.Name(),
		Namespace:     workload.Namespace().String(),
		Status:        workload.Status(),
		Ready:         fmt.Sprintf("%d/%d", workload.Ready(), workload.Desired()),
		Desired:       workload.Desired(),
		ReadyCount:    workload.Ready(),
		Current:       workload.Current(),
		Updated:       workload.Updated(),
		Available:     workload.Available(),
		IsHealthy:     workload.IsHealthy(),
		IsRolling:     workload.IsRolling(),
		Suspended:     workload.Suspended(),
		Images:        images,
		ControlledBy:  ownerLabel(workload.Owner()),
		Schedule:      workload.Schedule(),
		LastScheduled: formatTime(workload.LastScheduled()),
		Labels:        labels,
		CreatedAt:     formatTime(workload.CreatedAt()),
		AgeSeconds:    int64(workload.Age(now).Seconds()),
	}
}

func toWorkloads(workloads []domain.Workload, now time.Time) []Workload {
	out := make([]Workload, 0, len(workloads))
	for _, workload := range workloads {
		out = append(out, toWorkload(workload, now))
	}
	return out
}

// Event is a Kubernetes Event as presented to the UI.
type Event struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Type is "Normal" or "Warning".
	Type      string `json:"type"`
	IsWarning bool   `json:"isWarning"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	// InvolvedObject is the subject rendered as "Kind/name".
	InvolvedObject string `json:"involvedObject"`
	InvolvedKind   string `json:"involvedKind"`
	InvolvedName   string `json:"involvedName"`
	Source         string `json:"source"`
	Count          int32  `json:"count"`
	FirstSeen      string `json:"firstSeen"`
	LastSeen       string `json:"lastSeen"`
	AgeSeconds     int64  `json:"ageSeconds"`
}

func toEvent(event domain.Event, now time.Time) Event {
	return Event{
		Name:           event.Name(),
		Namespace:      event.Namespace().String(),
		Type:           string(event.Type()),
		IsWarning:      event.IsWarning(),
		Reason:         event.Reason(),
		Message:        event.Message(),
		InvolvedObject: event.InvolvedObject(),
		InvolvedKind:   event.InvolvedKind(),
		InvolvedName:   event.InvolvedName(),
		Source:         event.Source(),
		Count:          event.Count(),
		FirstSeen:      formatTime(event.FirstSeen()),
		LastSeen:       formatTime(event.LastSeen()),
		AgeSeconds:     int64(event.Age(now).Seconds()),
	}
}

func toEvents(events []domain.Event, now time.Time) []Event {
	out := make([]Event, 0, len(events))
	for _, event := range events {
		out = append(out, toEvent(event, now))
	}
	return out
}

// ResourceTable is a generically browsed kind, rendered with the columns the
// API server itself prints.
type ResourceTable struct {
	// KindID identifies what the table describes.
	KindID string `json:"kindId"`
	// Title is the plural display name of the kind.
	Title string `json:"title"`
	// Namespaced reports whether the rows carry namespaces.
	Namespaced bool          `json:"namespaced"`
	Columns    []TableColumn `json:"columns"`
	Rows       []TableRow    `json:"rows"`
}

// TableColumn describes one column of a generic table.
type TableColumn struct {
	Name string `json:"name"`
	// Type is "string", "integer", "number" or "date", so the UI can align
	// and sort correctly.
	Type string `json:"type"`
	// Wide marks columns from the extended set, which start hidden.
	Wide        bool   `json:"wide"`
	Description string `json:"description"`
}

// TableRow is one object rendered as cells.
type TableRow struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Cells     []string `json:"cells"`
}

func toResourceTable(table domain.ResourceTable) ResourceTable {
	kind := table.Kind()

	columns := make([]TableColumn, 0, len(table.Columns()))
	for _, column := range table.Columns() {
		columns = append(columns, TableColumn{
			Name:        column.Name,
			Type:        column.Type,
			Wide:        column.IsWide(),
			Description: column.Description,
		})
	}

	rows := make([]TableRow, 0, table.Len())
	for _, row := range table.Rows() {
		cells := row.Cells
		if cells == nil {
			cells = []string{}
		}
		rows = append(rows, TableRow{
			Name:      row.Name,
			Namespace: row.Namespace.String(),
			Cells:     cells,
		})
	}

	return ResourceTable{
		KindID:     kind.ID(),
		Title:      kind.Title,
		Namespaced: kind.Namespaced,
		Columns:    columns,
		Rows:       rows,
	}
}

// ownerLabel renders a controlling owner as "Kind/name".
func ownerLabel(owner domain.OwnerReference) string {
	if owner.IsZero() {
		return ""
	}
	return owner.Kind + "/" + owner.Name
}

// --- Formatting -------------------------------------------------------------
//
// Usage is formatted here rather than in the frontend so that every view shows
// the same number the same way, and so the rounding rules live next to the
// values they apply to.

// formatCores renders CPU the way `kubectl top` and Lens do: as a fraction of
// a core to three decimals. An unmeasured value renders as an em dash, never
// as "0.000", which would claim the workload is idle when nothing was measured.
func formatCores(metrics domain.Metrics) string {
	if metrics.IsZero() {
		return "—"
	}
	return fmt.Sprintf("%.3f", float64(metrics.CPUMilli)/1000)
}

// formatMilliCores renders a raw millicore count as cores.
func formatMilliCores(milli int64) string {
	if milli <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", float64(milli)/1000)
}

// formatMemory renders measured memory in binary units.
func formatMemory(metrics domain.Metrics) string {
	if metrics.IsZero() {
		return "—"
	}
	return formatBytes(metrics.MemoryBytes)
}

// binaryUnits are the suffixes Kubernetes itself uses for memory. Decimal
// units would disagree with every other tool an operator has open.
var binaryUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

// formatBytes renders a byte count in binary units, one decimal place.
func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "—"
	}

	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(binaryUnits)-1 {
		value /= 1024
		unit++
	}

	if unit == 0 {
		return fmt.Sprintf("%dB", bytes)
	}
	return fmt.Sprintf("%.1f%s", value, binaryUnits[unit])
}
