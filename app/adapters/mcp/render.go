package mcp

import (
	"fmt"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The projections below are what a tool returns.
//
// They are deliberately NOT the Wails DTOs. Those are the frontend's
// contract, shaped by what a Svelte component renders, and a second consumer
// reading them would make every change to a pane a change to this tool
// surface. They are also deliberately narrower than the domain: a model pays
// for every field it reads, and a pod's full manifest is one get_manifest
// call away when the summary is not enough.
//
// Ages are seconds rather than formatted strings — "3d" is a rendering
// decision the reader should make — and a time the domain never observed is
// omitted rather than sent as a zero timestamp.

// clusterRow is one kubeconfig context.
type clusterRow struct {
	Cluster          string `json:"cluster"`
	Server           string `json:"server"`
	Connected        bool   `json:"connected"`
	Current          bool   `json:"currentContext"`
	DefaultNamespace string `json:"defaultNamespace"`
	Version          string `json:"version,omitempty"`
}

// renderClusters lists what the kubeconfig names, marking what is open.
func renderClusters(clusters []domain.Cluster, connected map[domain.ClusterID]domain.Cluster) []clusterRow {
	rows := make([]clusterRow, 0, len(clusters))
	for _, cluster := range clusters {
		open, isOpen := connected[cluster.ID()]
		row := clusterRow{
			Cluster:          cluster.ID().String(),
			Server:           cluster.Server().Host(),
			Connected:        isOpen,
			Current:          cluster.IsCurrent(),
			DefaultNamespace: cluster.DefaultNamespace().String(),
			Version:          cluster.Version().GitVersion,
		}
		if isOpen && row.Version == "" {
			row.Version = open.Version().GitVersion
		}
		rows = append(rows, row)
	}
	return rows
}

// namespaceRow is one namespace.
type namespaceRow struct {
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	AgeSeconds int64  `json:"ageSeconds"`
}

func renderNamespaces(namespaces []domain.Namespace, now time.Time) []namespaceRow {
	rows := make([]namespaceRow, 0, len(namespaces))
	for _, namespace := range namespaces {
		rows = append(rows, namespaceRow{
			Name:       namespace.Name().String(),
			Phase:      string(namespace.Phase()),
			AgeSeconds: int64(namespace.Age(now).Seconds()),
		})
	}
	return rows
}

// kindRow is one browsable kind.
type kindRow struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	GroupVersion string `json:"groupVersion"`
	Namespaced   bool   `json:"namespaced"`
	Category     string `json:"category"`
	// Rich reports that PodSteer models this kind itself rather than asking
	// the API server to print it as a table. It changes which tool serves it
	// best — list_pods and list_workloads answer richly, list_resources
	// answers for anything — so the model is told rather than left to guess.
	Rich bool `json:"rich"`
}

func renderKinds(kinds []domain.ResourceKind) []kindRow {
	rows := make([]kindRow, 0, len(kinds))
	for _, kind := range kinds {
		rows = append(rows, kindRow{
			Kind:         kind.Kind,
			ID:           kind.ID(),
			GroupVersion: kind.GroupVersion(),
			Namespaced:   kind.Namespaced,
			Category:     string(kind.Category),
			Rich:         kind.Rich,
		})
	}
	return rows
}

// podRow is one pod, as the pod list shows it.
type podRow struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Phase        string `json:"phase"`
	Ready        string `json:"ready"`
	Restarts     int32  `json:"restarts"`
	Node         string `json:"node,omitempty"`
	PodIP        string `json:"podIP,omitempty"`
	QoS          string `json:"qosClass,omitempty"`
	ControlledBy string `json:"controlledBy,omitempty"`
	Status       string `json:"status"`
	Healthy      bool   `json:"healthy"`
	AgeSeconds   int64  `json:"ageSeconds"`
	// CPUMilli and MemoryBytes are omitted rather than zeroed when the
	// metrics API did not report this pod — a measured zero and no
	// measurement at all are different facts, and this is the codebase's
	// standing rule about metrics (see domain.Metrics.Measured).
	CPUMilli    *int64 `json:"cpuMilli,omitempty"`
	MemoryBytes *int64 `json:"memoryBytes,omitempty"`
}

func renderPods(pods []domain.Pod, now time.Time) []podRow {
	rows := make([]podRow, 0, len(pods))
	for _, pod := range pods {
		rows = append(rows, renderPod(pod, now))
	}
	return rows
}

func renderPod(pod domain.Pod, now time.Time) podRow {
	row := podRow{
		Name:       pod.Name(),
		Namespace:  pod.Namespace().String(),
		Phase:      string(pod.Phase()),
		Ready:      fmt.Sprintf("%d/%d", pod.ReadyContainers(), pod.TotalContainers()),
		Restarts:   pod.RestartCount(),
		Node:       pod.NodeName(),
		PodIP:      pod.PodIP(),
		QoS:        string(pod.QoSClass()),
		Status:     pod.StatusReason(),
		Healthy:    pod.IsHealthy(),
		AgeSeconds: int64(pod.Age(now).Seconds()),
	}

	if controller := pod.Controller(); !controller.IsZero() {
		row.ControlledBy = controller.Kind + "/" + controller.Name
	}
	if usage := pod.Usage(); usage.Measured {
		cpu, memory := usage.CPUMilli, usage.MemoryBytes
		row.CPUMilli, row.MemoryBytes = &cpu, &memory
	}

	return row
}

// containerRow is one container of one pod.
type containerRow struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	RestartCount int32  `json:"restarts"`
	LastExitCode *int32 `json:"lastExitCode,omitempty"`
	LastReason   string `json:"lastTerminationReason,omitempty"`
}

func renderContainers(containers []domain.Container) []containerRow {
	rows := make([]containerRow, 0, len(containers))
	for _, container := range containers {
		row := containerRow{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        container.Ready,
			State:        string(container.State),
			Reason:       container.Reason,
			RestartCount: container.RestartCount,
			LastReason:   container.LastTermination.Reason,
		}
		if container.LastTermination.Reason != "" {
			code := container.LastTermination.ExitCode
			row.LastExitCode = &code
		}
		rows = append(rows, row)
	}
	return rows
}

// workloadRow is one controller, as its list shows it.
type workloadRow struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace"`
	Desired       int32    `json:"desired"`
	Ready         int32    `json:"ready"`
	Updated       int32    `json:"updated"`
	Available     int32    `json:"available"`
	Status        string   `json:"status"`
	Healthy       bool     `json:"healthy"`
	Images        []string `json:"images,omitempty"`
	Suspended     bool     `json:"suspended,omitempty"`
	Schedule      string   `json:"schedule,omitempty"`
	ControlledBy  string   `json:"controlledBy,omitempty"`
	AgeSeconds    int64    `json:"ageSeconds"`
	RollingUpdate bool     `json:"rollingUpdate,omitempty"`
}

func renderWorkloads(workloads []domain.Workload, now time.Time) []workloadRow {
	rows := make([]workloadRow, 0, len(workloads))
	for _, workload := range workloads {
		row := workloadRow{
			Kind:          string(workload.Kind()),
			Name:          workload.Name(),
			Namespace:     workload.Namespace().String(),
			Desired:       workload.Desired(),
			Ready:         workload.Ready(),
			Updated:       workload.Updated(),
			Available:     workload.Available(),
			Status:        workload.Status(),
			Healthy:       workload.IsHealthy(),
			Images:        workload.Images(),
			Suspended:     workload.Suspended(),
			Schedule:      workload.Schedule(),
			AgeSeconds:    int64(workload.Age(now).Seconds()),
			RollingUpdate: workload.IsRolling(),
		}
		if owner := workload.Owner(); !owner.IsZero() {
			row.ControlledBy = owner.Kind + "/" + owner.Name
		}
		rows = append(rows, row)
	}
	return rows
}

// nodeRow is one node.
type nodeRow struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Ready          bool     `json:"ready"`
	Roles          []string `json:"roles,omitempty"`
	Unschedulable  bool     `json:"unschedulable,omitempty"`
	Conditions     []string `json:"activeConditions,omitempty"`
	KubeletVersion string   `json:"kubeletVersion,omitempty"`
	OSImage        string   `json:"osImage,omitempty"`
	Architecture   string   `json:"architecture,omitempty"`
	InternalIP     string   `json:"internalIP,omitempty"`
	CPUMilli       int64    `json:"allocatableCPUMilli"`
	MemoryBytes    int64    `json:"allocatableMemoryBytes"`
	MaxPods        int64    `json:"maxPods"`
	CPUPercent     *float64 `json:"cpuPercent,omitempty"`
	MemoryPercent  *float64 `json:"memoryPercent,omitempty"`
	AgeSeconds     int64    `json:"ageSeconds"`
}

func renderNodes(nodes []domain.Node, now time.Time) []nodeRow {
	rows := make([]nodeRow, 0, len(nodes))
	for _, node := range nodes {
		allocatable := node.Allocatable()
		row := nodeRow{
			Name:           node.Name(),
			Status:         node.Status(),
			Ready:          node.Ready(),
			Roles:          node.Roles(),
			Unschedulable:  node.Unschedulable(),
			Conditions:     conditionNames(node.ActiveConditions()),
			KubeletVersion: node.KubeletVersion(),
			OSImage:        node.OSImage(),
			Architecture:   node.Architecture(),
			InternalIP:     node.InternalIP(),
			CPUMilli:       allocatable.CPUMilli,
			MemoryBytes:    allocatable.MemoryBytes,
			MaxPods:        allocatable.Pods,
			AgeSeconds:     int64(node.Age(now).Seconds()),
		}
		if node.Usage().Measured {
			cpu, memory := node.CPUPercent(), node.MemoryPercent()
			row.CPUPercent, row.MemoryPercent = &cpu, &memory
		}
		rows = append(rows, row)
	}
	return rows
}

func conditionNames(conditions []domain.NodeCondition) []string {
	names := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		names = append(names, string(condition))
	}
	return names
}

// eventRow is one Kubernetes Event.
type eventRow struct {
	Type            string `json:"type"`
	Reason          string `json:"reason"`
	Message         string `json:"message"`
	Object          string `json:"object"`
	Namespace       string `json:"namespace,omitempty"`
	Source          string `json:"source,omitempty"`
	Count           int32  `json:"count"`
	LastSeenSeconds int64  `json:"lastSeenSecondsAgo"`
}

func renderEvents(events []domain.Event, now time.Time) []eventRow {
	rows := make([]eventRow, 0, len(events))
	for _, event := range events {
		rows = append(rows, eventRow{
			Type:            string(event.Type()),
			Reason:          event.Reason(),
			Message:         event.Message(),
			Object:          event.InvolvedObject(),
			Namespace:       event.Namespace().String(),
			Source:          event.Source(),
			Count:           event.Count(),
			LastSeenSeconds: int64(now.Sub(event.LastSeen()).Seconds()),
		})
	}
	return rows
}

// tableOut is the generic table, as the API server printed it.
type tableOut struct {
	Kind      string     `json:"kind"`
	KindID    string     `json:"kindId"`
	Columns   []string   `json:"columns"`
	Rows      []tableRow `json:"rows"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated,omitempty"`
}

// tableRow is one row of it.
type tableRow struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace,omitempty"`
	Cells     []string `json:"cells"`
}

func renderTable(table domain.ResourceTable, limit int) tableOut {
	columns := make([]string, 0, len(table.Columns()))
	for _, column := range table.Columns() {
		columns = append(columns, column.Name)
	}

	all := table.Rows()
	kept := all
	if len(kept) > limit {
		kept = kept[:limit]
	}

	rows := make([]tableRow, 0, len(kept))
	for _, row := range kept {
		rows = append(rows, tableRow{
			Name:      row.Name,
			Namespace: row.Namespace.String(),
			Cells:     row.Cells,
		})
	}

	return tableOut{
		Kind:      table.Kind().Kind,
		KindID:    table.Kind().ID(),
		Columns:   columns,
		Rows:      rows,
		Total:     len(all),
		Truncated: len(kept) < len(all),
	}
}

// findingOut is one cluster finding.
type findingOut struct {
	ID            string       `json:"id"`
	Severity      string       `json:"severity"`
	Category      string       `json:"category"`
	Title         string       `json:"title"`
	Summary       string       `json:"summary"`
	Advice        string       `json:"advice"`
	Count         int          `json:"count"`
	OldestSeconds int64        `json:"oldestSeconds,omitempty"`
	Subjects      []subjectOut `json:"subjects,omitempty"`
}

// subjectOut is one object a finding is about.
type subjectOut struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
}

func renderFindings(findings []domain.Finding) []findingOut {
	rendered := make([]findingOut, 0, len(findings))
	for _, finding := range findings {
		subjects := make([]subjectOut, 0, len(finding.Subjects))
		for _, subject := range finding.Subjects {
			subjects = append(subjects, subjectOut{
				Kind:      subject.Kind,
				Namespace: subject.Namespace.String(),
				Name:      subject.Name,
				Detail:    subject.Detail,
			})
		}
		rendered = append(rendered, findingOut{
			ID:            finding.ID,
			Severity:      string(finding.Severity),
			Category:      string(finding.Category),
			Title:         finding.Title,
			Summary:       finding.Summary,
			Advice:        finding.Advice,
			Count:         finding.Count,
			OldestSeconds: finding.OldestSeconds,
			// Subjects are already truncated by the domain at maxSubjects,
			// and Count is the complete number — the same completeness rule
			// the folded dependency map keeps.
			Subjects: subjects,
		})
	}
	return rendered
}

// overviewOut is the cluster assessment.
type overviewOut struct {
	Cluster     string       `json:"cluster"`
	Version     string       `json:"version,omitempty"`
	Health      string       `json:"health"`
	GeneratedAt string       `json:"generatedAt"`
	Findings    []findingOut `json:"findings"`
	Nodes       nodesOut     `json:"nodes"`
	Pods        podsOut      `json:"pods"`
	Capacity    capacityOut  `json:"capacity"`
	// Metrics says WHY usage figures are absent when they are: an
	// uninstalled metrics-server and an unreadable one call for opposite
	// advice, and a reader told only "no metrics" gives the wrong one.
	Metrics string `json:"metrics"`
	// Unavailable names the sources that could not be read. An assessment is
	// deliberately not refused because one source failed, so this is how a
	// partial answer says it is partial.
	Unavailable []string `json:"unavailable,omitempty"`
}

type nodesOut struct {
	Total         int `json:"total"`
	Ready         int `json:"ready"`
	NotReady      int `json:"notReady"`
	Cordoned      int `json:"cordoned"`
	UnderPressure int `json:"underPressure"`
	ControlPlane  int `json:"controlPlane"`
}

type podsOut struct {
	Total       int   `json:"total"`
	Running     int   `json:"running"`
	Pending     int   `json:"pending"`
	Succeeded   int   `json:"succeeded"`
	Failed      int   `json:"failed"`
	Terminating int   `json:"terminating"`
	NotReady    int   `json:"notReady"`
	Restarts    int32 `json:"restarts"`
}

// capacityOut reports what the cluster can still schedule.
//
// REQUESTS AND USAGE BOTH, never one of them: requests decide what schedules,
// usage decides what is actually being consumed, and a cluster can refuse
// pods while every usage figure looks calm.
type capacityOut struct {
	CPU    resourceOut `json:"cpu"`
	Memory resourceOut `json:"memory"`
	Pods   podSlotsOut `json:"podSlots"`
}

type resourceOut struct {
	Allocatable int64 `json:"allocatable"`
	Requests    int64 `json:"requests"`
	Limits      int64 `json:"limits"`
	// Usage is measured across the NODES and so includes the kubelet, the
	// runtime and the OS; PodUsage is summed across pods only. Both, because
	// dividing the first by requests reports a cluster as over 100%
	// efficient — see domain.ResourceUsage.
	Usage    int64 `json:"usage"`
	PodUsage int64 `json:"podUsage"`
}

// renderResource projects one dimension of the capacity summary.
func renderResource(usage domain.ResourceUsage) resourceOut {
	return resourceOut{
		Allocatable: usage.Allocatable,
		Requests:    usage.Requests,
		Limits:      usage.Limits,
		Usage:       usage.Usage,
		PodUsage:    usage.PodUsage,
	}
}

// podSlotsOut counts pod slots, not pods.
//
// Capacity is the sum of the pod limits of nodes a pod could actually land
// on — ready, uncordoned and carrying no blocking taint — so a control
// plane's slots are not counted as headroom nothing can reach.
type podSlotsOut struct {
	Capacity  int64 `json:"capacity"`
	Scheduled int   `json:"scheduled"`
	Healthy   int   `json:"healthy"`
}

func renderOverview(overview domain.Overview) overviewOut {
	return overviewOut{
		Cluster:     overview.ClusterID.String(),
		Version:     overview.Version.GitVersion,
		Health:      string(overview.Health),
		GeneratedAt: overview.GeneratedAt.UTC().Format(time.RFC3339),
		Findings:    renderFindings(overview.Findings),
		Nodes: nodesOut{
			Total:         overview.Nodes.Total,
			Ready:         overview.Nodes.Ready,
			NotReady:      overview.Nodes.NotReady,
			Cordoned:      overview.Nodes.Cordoned,
			UnderPressure: overview.Nodes.UnderPressure,
			ControlPlane:  overview.Nodes.ControlPlane,
		},
		Pods: podsOut{
			Total:       overview.Pods.Total,
			Running:     overview.Pods.Running,
			Pending:     overview.Pods.Pending,
			Succeeded:   overview.Pods.Succeeded,
			Failed:      overview.Pods.Failed,
			Terminating: overview.Pods.Terminating,
			NotReady:    overview.Pods.NotReady,
			Restarts:    overview.Pods.Restarts,
		},
		Capacity: capacityOut{
			CPU:    renderResource(overview.Capacity.CPU),
			Memory: renderResource(overview.Capacity.Memory),
			Pods: podSlotsOut{
				Capacity:  overview.Capacity.Pods.Capacity,
				Scheduled: overview.Capacity.Pods.Scheduled,
				Healthy:   overview.Capacity.Pods.Healthy,
			},
		},
		Metrics:     string(overview.Metrics),
		Unavailable: overview.Unavailable,
	}
}

// podAssessmentOut is one pod's own assessment.
type podAssessmentOut struct {
	Pod        podRow            `json:"pod"`
	Containers []containerRow    `json:"containers"`
	Findings   []podFindingOut   `json:"findings"`
	Conditions []podConditionOut `json:"conditions,omitempty"`
}

// podFindingOut is one finding about that pod.
type podFindingOut struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice"`
}

type podConditionOut struct {
	Type   string `json:"type"`
	Status bool   `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func renderConditions(conditions []domain.PodCondition) []podConditionOut {
	rendered := make([]podConditionOut, 0, len(conditions))
	for _, condition := range conditions {
		rendered = append(rendered, podConditionOut{
			Type:   condition.Type,
			Status: condition.True(),
			Reason: condition.Reason,
		})
	}
	return rendered
}

func renderPodFindings(findings []domain.PodFinding) []podFindingOut {
	rendered := make([]podFindingOut, 0, len(findings))
	for _, finding := range findings {
		rendered = append(rendered, podFindingOut{
			Severity: string(finding.Severity),
			Title:    finding.Title,
			Detail:   finding.Detail,
			Advice:   finding.Advice,
		})
	}
	return rendered
}

// graphOut is a dependency map.
type graphOut struct {
	Nodes []graphNodeOut `json:"nodes"`
	Edges []graphEdgeOut `json:"edges"`
	// Unreadable names what a refusal hid. A box that is missing because
	// nobody may read it is not a box that is not there.
	Unreadable []string `json:"unreadable,omitempty"`
	// Bounded says that nothing was LOOKED for below this object, which is
	// not the same as nothing being found — see domain.DownwardBound.
	Bounded string `json:"bounded,omitempty"`
}

type graphNodeOut struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	APIKind   string `json:"apiKind,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Healthy   bool   `json:"healthy"`
	Subject   bool   `json:"subject,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

type graphEdgeOut struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

func renderGraph(graph domain.PodGraph) graphOut {
	nodes := make([]graphNodeOut, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes = append(nodes, graphNodeOut{
			ID:        node.ID,
			Kind:      string(node.Kind),
			APIKind:   node.APIKind,
			Name:      node.Name,
			Namespace: node.Namespace,
			Detail:    node.Detail,
			Healthy:   node.Healthy,
			Subject:   node.Subject,
			Missing:   node.Missing,
		})
	}

	edges := make([]graphEdgeOut, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		edges = append(edges, graphEdgeOut{From: edge.From, To: edge.To, Label: edge.Label})
	}

	return graphOut{Nodes: nodes, Edges: edges, Unreadable: graph.Unreadable, Bounded: graph.Bounded}
}

// ruleOut is one RBAC policy rule, quoted as the API server stated it.
type ruleOut struct {
	Verbs           []string `json:"verbs,omitempty"`
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
}

func renderRules(rules []domain.PolicyRule) []ruleOut {
	rendered := make([]ruleOut, 0, len(rules))
	for _, rule := range rules {
		rendered = append(rendered, ruleOut{
			Verbs:           rule.Verbs,
			APIGroups:       rule.APIGroups,
			Resources:       rule.Resources,
			ResourceNames:   rule.ResourceNames,
			NonResourceURLs: rule.NonResourceURLs,
		})
	}
	return rendered
}

// subjectRulesOut is what the current credentials may do in one namespace.
type subjectRulesOut struct {
	Namespace string `json:"namespace"`
	// Status separates an answered review from a refused one from an absent
	// API — the same distinction domain.ReviewStatus exists to keep, and
	// collapsing it would let "forbidden" read as "you may do nothing".
	Status      string    `json:"status"`
	Refusal     string    `json:"refusal,omitempty"`
	Resource    []ruleOut `json:"resourceRules,omitempty"`
	NonResource []ruleOut `json:"nonResourceRules,omitempty"`
	Incomplete  bool      `json:"incomplete,omitempty"`
	Reason      string    `json:"incompleteReason,omitempty"`
}

func renderSubjectRules(rules domain.SubjectRules) subjectRulesOut {
	return subjectRulesOut{
		Namespace:   rules.Namespace.String(),
		Status:      string(rules.Status),
		Refusal:     rules.Refusal,
		Resource:    renderRules(rules.Review.Resource),
		NonResource: renderRules(rules.Review.NonResource),
		Incomplete:  rules.Review.Incomplete,
		Reason:      rules.Review.IncompleteReason,
	}
}

// accessDecisionOut is one access review, as the API server answered it.
type accessDecisionOut struct {
	Status  string `json:"status"`
	Refusal string `json:"refusal,omitempty"`
	// Allowed and Denied are BOTH carried, because they are not opposites:
	// an authorizer with no opinion leaves both false, and rendering that as
	// a denial claims a verdict nothing gave.
	Allowed         bool   `json:"allowed"`
	Denied          bool   `json:"denied"`
	Reason          string `json:"reason,omitempty"`
	EvaluationError string `json:"evaluationError,omitempty"`
}

func renderAccessDecision(decision domain.AccessDecision) accessDecisionOut {
	return accessDecisionOut{
		Status:          string(decision.Status),
		Refusal:         decision.Refusal,
		Allowed:         decision.Outcome.Allowed,
		Denied:          decision.Outcome.Denied,
		Reason:          decision.Outcome.Reason,
		EvaluationError: decision.Outcome.EvaluationError,
	}
}

// roleInspectionOut is one Role or ClusterRole with what it permits.
type roleInspectionOut struct {
	Scope   string    `json:"scope"`
	Name    string    `json:"name"`
	Status  string    `json:"status"`
	Refusal string    `json:"refusal,omitempty"`
	Rules   []ruleOut `json:"rules,omitempty"`
	// BindingsStatus is separate from Status because an account routinely
	// reads a role without being able to list the bindings to it, and one
	// refusal must not blank the half that answered.
	BindingsStatus  string        `json:"bindingsStatus"`
	BindingsRefusal string        `json:"bindingsRefusal,omitempty"`
	Bindings        []bindingOut  `json:"bindings,omitempty"`
	Findings        []rbacFinding `json:"findings,omitempty"`
}

type bindingOut struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Namespace string   `json:"namespace,omitempty"`
	Subjects  []string `json:"subjects,omitempty"`
}

type rbacFinding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice"`
}

func renderRoleInspection(inspection domain.RoleInspection) roleInspectionOut {
	bindings := make([]bindingOut, 0, len(inspection.Bindings))
	for _, binding := range inspection.Bindings {
		subjects := make([]string, 0, len(binding.Subjects))
		for _, subject := range binding.Subjects {
			name := string(subject.Kind) + "/" + subject.Name
			if !subject.Namespace.IsAll() {
				name = string(subject.Kind) + "/" + subject.Namespace.String() + "/" + subject.Name
			}
			subjects = append(subjects, name)
		}
		bindings = append(bindings, bindingOut{
			Kind:      binding.Kind,
			Name:      binding.Name,
			Namespace: binding.Namespace.String(),
			Subjects:  subjects,
		})
	}

	findings := make([]rbacFinding, 0, len(inspection.Findings))
	for _, finding := range inspection.Findings {
		findings = append(findings, rbacFinding{
			Severity: string(finding.Severity),
			Title:    finding.Title,
			Detail:   finding.Detail,
			Advice:   finding.Advice,
		})
	}

	return roleInspectionOut{
		Scope:           string(inspection.Target.Scope),
		Name:            inspection.Target.Name,
		Status:          string(inspection.Status),
		Refusal:         inspection.Refusal,
		Rules:           renderRules(inspection.Rules),
		BindingsStatus:  string(inspection.BindingsStatus),
		BindingsRefusal: inspection.BindingsRefusal,
		Bindings:        bindings,
		Findings:        findings,
	}
}

// inventoryOut is what one namespace holds, kind by kind.
type inventoryOut struct {
	Namespace string         `json:"namespace"`
	Counts    []inventoryRow `json:"counts"`
	Empty     int            `json:"emptyKinds"`
}

// inventoryRow is one kind's count.
//
// Count is a pointer so an unreadable kind carries no number at all: a
// refused count is not zero, and an account without `list secrets` must be
// told the Secrets count is unknown rather than shown a nought.
type inventoryRow struct {
	Kind       string `json:"kind"`
	Count      *int   `json:"count,omitempty"`
	Unreadable string `json:"unreadable,omitempty"`
}

func renderInventory(inventory domain.NamespaceInventory) inventoryOut {
	rows := make([]inventoryRow, 0, len(inventory.Counts))
	for _, count := range inventory.Counts {
		row := inventoryRow{Kind: count.Kind.Kind, Unreadable: count.Unreadable}
		if count.Unreadable == "" {
			total := count.Count
			row.Count = &total
		}
		rows = append(rows, row)
	}
	return inventoryOut{
		Namespace: inventory.Namespace.String(),
		Counts:    rows,
		Empty:     inventory.Empty,
	}
}
