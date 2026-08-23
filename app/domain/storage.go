package domain

// Persistent storage: the volumes a cluster has provisioned and the claims
// that asked for them.
//
// Modelled as two types rather than one because they fail apart. A claim that
// never binds is a workload stuck at ContainerCreating with no pod-level
// symptom to read; a volume left Released is storage the cluster no longer
// uses and the cloud provider still bills for. Neither shows up anywhere else
// in an overview, and both are found by looking at the pair.

import (
	"strings"
	"time"
)

// ClaimPhase is where a PersistentVolumeClaim has got to.
type ClaimPhase string

const (
	// ClaimPending means no volume has been bound yet.
	ClaimPending ClaimPhase = "Pending"
	// ClaimBound means storage is attached and usable.
	ClaimBound ClaimPhase = "Bound"
	// ClaimLost means the volume behind it has gone.
	ClaimLost ClaimPhase = "Lost"
)

// VolumePhase is where a PersistentVolume has got to.
type VolumePhase string

const (
	// VolumeAvailable means the volume exists and nothing claims it.
	VolumeAvailable VolumePhase = "Available"
	// VolumeBound means a claim is using it.
	VolumeBound VolumePhase = "Bound"
	// VolumeReleased means its claim is gone but the volume remains, which is
	// what a Retain reclaim policy is for — and what quietly accumulates cost.
	VolumeReleased VolumePhase = "Released"
	// VolumeFailed means automatic reclamation failed.
	VolumeFailed VolumePhase = "Failed"
)

// bindingGrace is how long a claim may sit Pending before it is worth saying.
//
// Binding is not instantaneous: a cloud provider provisioning a disk takes
// tens of seconds, and WaitForFirstConsumer claims stay Pending by design
// until a pod schedules. Reporting either as a fault would make the finding
// noise on a healthy cluster.
const bindingGrace = 2 * time.Minute

// PersistentVolumeClaimSpec is the data a claim is built from.
type PersistentVolumeClaimSpec struct {
	Name         string
	Namespace    NamespaceName
	ClusterID    ClusterID
	Phase        ClaimPhase
	StorageClass string
	// RequestedBytes is what the workload asked for.
	RequestedBytes int64
	// CapacityBytes is what it actually got, which can exceed the request:
	// providers round up to their own increments.
	CapacityBytes int64
	VolumeName    string
	CreatedAt     time.Time
}

// PersistentVolumeClaim is a workload's request for storage.
type PersistentVolumeClaim struct {
	name           string
	namespace      NamespaceName
	clusterID      ClusterID
	phase          ClaimPhase
	storageClass   string
	requestedBytes int64
	capacityBytes  int64
	volumeName     string
	createdAt      time.Time
}

// NewPersistentVolumeClaim validates and builds a claim.
func NewPersistentVolumeClaim(spec PersistentVolumeClaimSpec) (PersistentVolumeClaim, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return PersistentVolumeClaim{}, ErrEmptyResourceName
	}
	if spec.ClusterID.IsZero() {
		return PersistentVolumeClaim{}, ErrEmptyClusterID
	}

	return PersistentVolumeClaim{
		name:           name,
		namespace:      spec.Namespace,
		clusterID:      spec.ClusterID,
		phase:          spec.Phase,
		storageClass:   spec.StorageClass,
		requestedBytes: spec.RequestedBytes,
		capacityBytes:  spec.CapacityBytes,
		volumeName:     spec.VolumeName,
		createdAt:      spec.CreatedAt.UTC(),
	}, nil
}

// Name returns the claim's name.
func (c PersistentVolumeClaim) Name() string { return c.name }

// Namespace returns the namespace it lives in.
func (c PersistentVolumeClaim) Namespace() NamespaceName { return c.namespace }

// Phase returns where the claim has got to.
func (c PersistentVolumeClaim) Phase() ClaimPhase { return c.phase }

// StorageClass returns the class that provisions it, empty when none was set.
func (c PersistentVolumeClaim) StorageClass() string { return c.storageClass }

// RequestedBytes returns what the workload asked for.
func (c PersistentVolumeClaim) RequestedBytes() int64 { return c.requestedBytes }

// CapacityBytes returns what was actually provisioned, falling back to the
// request while nothing is bound yet.
func (c PersistentVolumeClaim) CapacityBytes() int64 {
	if c.capacityBytes > 0 {
		return c.capacityBytes
	}
	return c.requestedBytes
}

// Age returns how long the claim has existed.
func (c PersistentVolumeClaim) Age(now time.Time) time.Duration {
	if c.createdAt.IsZero() {
		return 0
	}
	return now.Sub(c.createdAt)
}

// StuckPending reports a claim that has waited longer than binding takes.
//
// The grace period is what separates a claim being provisioned right now from
// one that will never bind — a missing storage class, a zone with no capacity,
// a quota already spent.
func (c PersistentVolumeClaim) StuckPending(now time.Time) bool {
	return c.phase == ClaimPending && c.Age(now) > bindingGrace
}

// PersistentVolumeSpec is the data a volume is built from.
type PersistentVolumeSpec struct {
	Name          string
	ClusterID     ClusterID
	Phase         VolumePhase
	StorageClass  string
	CapacityBytes int64
	// ReclaimPolicy decides what happens to the volume when its claim goes:
	// Delete removes it, Retain keeps it and its data, Recycle is long gone.
	ReclaimPolicy string
	// ClaimRef names the claim using it, empty when nothing does.
	ClaimRef  string
	CreatedAt time.Time
}

// PersistentVolume is a piece of storage the cluster has provisioned.
type PersistentVolume struct {
	name          string
	clusterID     ClusterID
	phase         VolumePhase
	storageClass  string
	capacityBytes int64
	reclaimPolicy string
	claimRef      string
	createdAt     time.Time
}

// NewPersistentVolume validates and builds a volume.
func NewPersistentVolume(spec PersistentVolumeSpec) (PersistentVolume, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return PersistentVolume{}, ErrEmptyResourceName
	}
	if spec.ClusterID.IsZero() {
		return PersistentVolume{}, ErrEmptyClusterID
	}

	return PersistentVolume{
		name:          name,
		clusterID:     spec.ClusterID,
		phase:         spec.Phase,
		storageClass:  spec.StorageClass,
		capacityBytes: spec.CapacityBytes,
		reclaimPolicy: spec.ReclaimPolicy,
		claimRef:      spec.ClaimRef,
		createdAt:     spec.CreatedAt.UTC(),
	}, nil
}

// Name returns the volume's name.
func (v PersistentVolume) Name() string { return v.name }

// Phase returns where the volume has got to.
func (v PersistentVolume) Phase() VolumePhase { return v.phase }

// StorageClass returns the class it belongs to.
func (v PersistentVolume) StorageClass() string { return v.storageClass }

// CapacityBytes returns its size.
func (v PersistentVolume) CapacityBytes() int64 { return v.capacityBytes }

// ReclaimPolicy returns what happens to it when its claim goes.
func (v PersistentVolume) ReclaimPolicy() string { return v.reclaimPolicy }

// ClaimRef names the claim bound to it, or empty.
func (v PersistentVolume) ClaimRef() string { return v.claimRef }

// Age returns how long the volume has existed.
func (v PersistentVolume) Age(now time.Time) time.Duration {
	if v.createdAt.IsZero() {
		return 0
	}
	return now.Sub(v.createdAt)
}

// Orphaned reports a volume nothing is using that will not clean itself up.
//
// Released with a Retain policy is the expensive case and the reason this is
// worth reporting at all: the claim is gone, the data is kept deliberately,
// and nothing will ever remove it. On a cloud provider the disk is still
// billed, month after month, and no list in any Kubernetes client points at it.
func (v PersistentVolume) Orphaned() bool {
	return v.phase == VolumeReleased && !strings.EqualFold(v.reclaimPolicy, "Delete")
}
