package domain

import "slices"

// What a workload's own declaration says about the privileges it takes.
//
// QUOTED, NOT JUDGED — the same relationship vulnerability.go has with a
// scanner, applied to a source PodSteer does read itself. Every field here is
// a value the operator wrote into a pod spec; nothing is inferred, scored or
// weighted, and the findings built from them (see securityFindings in
// overview.go) name the field that produced them.
//
// WHY THIS IS NOT A CVE PROBLEM, which is the distinction the whole design
// rests on. A vulnerability finding depends on an advisory database that
// somebody else curates, that disagrees with the next database, and that
// PodSteer has no business owning — so those are read from a scanner the
// operator chose. A privileged container is a fact about a manifest in front
// of us. It needs no feed, it will mean the same thing in five years, and the
// rule that finds it is three lines long.

// ContainerSecurity is one container's securityContext, in the fields a
// posture check reads.
//
// POINTERS FOR THE TRISTATE FIELDS, deliberately. Kubernetes distinguishes
// "false", "true" and "not stated", and the third is the common case: an
// absent allowPrivilegeEscalation is not a claim that escalation is
// forbidden, it is the operator not having said. Collapsing that to a bool
// would make every unset field read as a deliberate "no" — and a check built
// on it would either flag the whole cluster or nothing at all.
type ContainerSecurity struct {
	// Privileged is spec.securityContext.privileged.
	Privileged *bool
	// AllowPrivilegeEscalation is the field of the same name. Nil means
	// unstated, which Kubernetes treats as true for an unprivileged container
	// — but unstated is not the same act as writing true, and only the
	// written one is reported.
	AllowPrivilegeEscalation *bool
	// RunAsUser is the UID the container is pinned to, when one is stated.
	RunAsUser *int64
	// RunAsNonRoot is the field of the same name.
	RunAsNonRoot *bool
	// AddedCapabilities are the Linux capabilities the container asks for
	// beyond the runtime's default set, verbatim and uppercased as written.
	AddedCapabilities []string
}

// PodSecurity is the pod-level half: the namespaces it shares with its node.
//
// These three are the sharpest signals a spec carries, because none of them
// has a benign default and each one is a deliberate act. A pod that shares the
// host's PID namespace can see and signal every process on that node.
type PodSecurity struct {
	HostNetwork bool
	HostPID     bool
	HostIPC     bool
}

// dangerousCapabilities are the additions worth reporting on their own.
//
// NOT EVERY CAPABILITY, and the shortness of this list is the point. A
// container adding NET_BIND_SERVICE to listen on port 80 is ordinary and
// reporting it teaches an operator to ignore the category. Each of these
// grants something that materially changes what a compromise of the container
// reaches:
//
//   - SYS_ADMIN is the capability that is a synonym for root; it is what
//     mounting, and most container escapes, need.
//   - SYS_MODULE loads kernel modules — a compromise becomes the kernel.
//   - SYS_PTRACE reads the memory of other processes, which with hostPID is
//     every process on the node.
//   - SYS_BOOT reboots the node.
//   - NET_ADMIN and NET_RAW reconfigure the node's networking and forge
//     packets on it.
//   - DAC_READ_SEARCH bypasses file permission checks, and is the classic
//     path to reading a host filesystem through an open file descriptor.
//
// "ALL" is here because `capabilities.add: ["ALL"]` is written more often
// than anybody expects, and it is every one of the above at once.
var dangerousCapabilities = []string{
	"ALL",
	"SYS_ADMIN",
	"SYS_MODULE",
	"SYS_PTRACE",
	"SYS_BOOT",
	"NET_ADMIN",
	"NET_RAW",
	"DAC_READ_SEARCH",
}

// IsPrivileged reports whether the container asked for privileged mode.
func (c ContainerSecurity) IsPrivileged() bool {
	return c.Privileged != nil && *c.Privileged
}

// AllowsEscalation reports whether the spec WROTE allowPrivilegeEscalation
// true. An unstated field is not reported — see the field's own comment.
func (c ContainerSecurity) AllowsEscalation() bool {
	return c.AllowPrivilegeEscalation != nil && *c.AllowPrivilegeEscalation
}

// RunsAsRoot reports whether the spec pins the container to UID 0, or states
// outright that it need not be a non-root user.
//
// AN UNSTATED runAsUser IS NOT REPORTED, and that is a deliberate limit on
// what this can claim: the effective user then comes from the image, which
// PodSteer does not read — see ADR 9, no registry is read from the client. A
// finding that said "runs as root" on the strength of an absent field would be
// guessing about a layer nobody here has looked at.
func (c ContainerSecurity) RunsAsRoot() bool {
	if c.RunAsUser != nil && *c.RunAsUser == 0 {
		return true
	}
	return c.RunAsNonRoot != nil && !*c.RunAsNonRoot
}

// DangerousCapabilities returns the added capabilities worth reporting, in the
// order this package ranks them.
func (c ContainerSecurity) DangerousCapabilities() []string {
	if len(c.AddedCapabilities) == 0 {
		return nil
	}

	found := make([]string, 0, len(c.AddedCapabilities))
	for _, capability := range dangerousCapabilities {
		if slices.Contains(c.AddedCapabilities, capability) {
			found = append(found, capability)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// SharesHostNamespace reports whether the pod shares any namespace with its
// node, and names the ones it shares.
func (p PodSecurity) SharesHostNamespace() []string {
	shared := make([]string, 0, 3)
	if p.HostNetwork {
		shared = append(shared, "network")
	}
	if p.HostPID {
		shared = append(shared, "PID")
	}
	if p.HostIPC {
		shared = append(shared, "IPC")
	}
	if len(shared) == 0 {
		return nil
	}
	return shared
}
