package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// named returns the attached names of one kind, for asserting what was found.
func named(refs []domain.AttachedRef, kind domain.GraphKind) []string {
	var out []string
	for _, ref := range refs {
		if ref.Kind == kind {
			out = append(out, ref.Name)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAProjectedVolumeIsReadForEverySourceInIt(t *testing.T) {
	// A PROJECTED VOLUME IS SEVERAL SOURCES IN ONE MOUNT, and it is how a
	// great many pods actually read their configuration. Matching only on
	// `volume.ConfigMap` missed all of them, so those dependencies never
	// appeared on the map at all.
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:         "app",
			VolumeMounts: []corev1.VolumeMount{{Name: "bundle", MountPath: "/etc/bundle"}},
		}},
		Volumes: []corev1.Volume{{
			Name: "bundle",
			VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{
					{ConfigMap: &corev1.ConfigMapProjection{
						LocalObjectReference: corev1.LocalObjectReference{Name: "ca-bundle"},
					}},
					{Secret: &corev1.SecretProjection{
						LocalObjectReference: corev1.LocalObjectReference{Name: "api-token"},
					}},
					// A service-account token projection names no object of
					// its own and must not invent one.
					{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token"}},
				},
			}},
		}},
	}

	refs := attachedFromSpec(spec)

	if !contains(named(refs, domain.GraphConfig), "ca-bundle") {
		t.Error("the projected ConfigMap was not found")
	}
	if !contains(named(refs, domain.GraphSecret), "api-token") {
		t.Error("the projected Secret was not found")
	}
	if len(refs) != 2 {
		t.Errorf("found %d dependencies, want 2 — a token projection names no object", len(refs))
	}
}

func TestImagePullSecretsAreDependencies(t *testing.T) {
	// One of the few whose absence stops a pod before any of its own
	// configuration is read, and nothing in the volume list mentions them.
	spec := &corev1.PodSpec{
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry-creds"}},
		Containers:       []corev1.Container{{Name: "app"}},
	}

	if !contains(named(attachedFromSpec(spec), domain.GraphSecret), "registry-creds") {
		t.Error("the image pull secret was not treated as a dependency")
	}
}

func TestAnEphemeralVolumeIsStorage(t *testing.T) {
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: "app"}},
		Volumes: []corev1.Volume{{
			Name:         "scratch",
			VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}},
		}},
	}

	if !contains(named(attachedFromSpec(spec), domain.GraphClaim), "scratch") {
		t.Error("a generic ephemeral volume was not treated as storage")
	}
}

func TestEnvironmentReferencesCountAsMuchAsMounts(t *testing.T) {
	// A ConfigMap read through envFrom is as much a dependency as one mounted
	// at a path, and it is the one people forget because nothing in the volume
	// list mentions it.
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{
			Name: "app",
			EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "settings"},
			}}},
			Env: []corev1.EnvVar{{
				Name: "PASSWORD",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "db-password"},
				}},
			}},
		}},
	}

	refs := attachedFromSpec(spec)
	if !contains(named(refs, domain.GraphConfig), "settings") {
		t.Error("an envFrom ConfigMap was not found")
	}
	if !contains(named(refs, domain.GraphSecret), "db-password") {
		t.Error("a secretKeyRef was not found")
	}
}

func TestInitContainerDependenciesAreRead(t *testing.T) {
	// An init container that cannot find its Secret stops the pod before the
	// application container starts, so its dependencies are the pod's.
	spec := &corev1.PodSpec{
		InitContainers: []corev1.Container{{
			Name: "migrate",
			EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "migration-creds"},
			}}},
		}},
		Containers: []corev1.Container{{Name: "app"}},
	}

	if !contains(named(attachedFromSpec(spec), domain.GraphSecret), "migration-creds") {
		t.Error("an init container's Secret was not treated as a dependency")
	}
}

func TestTheDefaultServiceAccountIsNotDrawn(t *testing.T) {
	// Every pod runs as one, so drawing it would add a box to every map that
	// distinguishes nothing. A named account is a choice somebody made.
	plain := attachedFromSpec(&corev1.PodSpec{ServiceAccountName: "default"})
	if len(named(plain, domain.GraphServiceAccount)) != 0 {
		t.Error("the default service account was drawn")
	}

	chosen := attachedFromSpec(&corev1.PodSpec{ServiceAccountName: "deployer"})
	if !contains(named(chosen, domain.GraphServiceAccount), "deployer") {
		t.Error("a named service account was not drawn")
	}
}

func TestOwnedByMatchesOnKindAndName(t *testing.T) {
	// Pods are claimed by ownerReference rather than by the `job-name` label:
	// the label is just a label, and a pod relabelled by hand would otherwise
	// be claimed by a Job that never created it.
	owners := []metav1.OwnerReference{{Kind: "Job", Name: "nightly-28"}}

	if !ownedBy(owners, "Job", "nightly-28") {
		t.Error("an owner that matches was not recognised")
	}
	if ownedBy(owners, "Job", "nightly-29") {
		t.Error("a different job's name matched")
	}
	if ownedBy(owners, "CronJob", "nightly-28") {
		t.Error("a different kind matched on name alone")
	}
}
