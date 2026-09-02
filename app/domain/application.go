package domain

import (
	"cmp"
	"slices"
)

// Grouping a cluster's objects by the application they belong to.
//
// KUBERNETES DOES STANDARDISE THIS, and it is the only part of it that is
// standardised: the recommended labels, `app.kubernetes.io/*`. `instance`
// names one deployed copy of an application — the unit somebody means by "the
// bill-registry-service in development" — and `part-of` names the larger
// thing it belongs to, which is how a platform of thirty services says it is
// one platform.
//
// THE LABELS ARE A CONVENTION, NOT A GUARANTEE, and that shapes everything
// here. They are voluntary; a chart that does not set them, or a hand-written
// manifest, is invisible to any grouping built on them. So the count of what
// was NOT grouped is carried alongside the groups and shown — a view that
// silently omits a third of a namespace is worse than no view, because it
// looks complete.
//
// Nothing is inferred. A name that looks like a prefix of another name is not
// evidence of anything, and guessing membership from naming conventions is
// how a client shows somebody a group that does not exist.

// The recommended labels this reads. Their meanings are Kubernetes', not ours.
const (
	// LabelInstance names a unique deployed copy of an application.
	LabelInstance = "app.kubernetes.io/instance"
	// LabelPartOf names the wider application this is a component of.
	LabelPartOf = "app.kubernetes.io/part-of"
	// LabelName names the application's software, shared by every instance.
	LabelName = "app.kubernetes.io/name"
	// LabelComponent names the role within the architecture.
	LabelComponent = "app.kubernetes.io/component"
	// LabelManagedBy names the tool that deploys it.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelVersion is the software's own version.
	LabelVersion = "app.kubernetes.io/version"
)

// ApplicationMember is one kind's contribution to an application.
type ApplicationMember struct {
	// Kind is the Kubernetes kind, e.g. "Deployment".
	Kind string
	// Count is how many of them belong to the application.
	Count int
}

// Application is one deployed instance, and what it is made of.
type Application struct {
	// Instance is the app.kubernetes.io/instance label — the identity.
	Instance string
	// Namespace is where it runs. An instance deployed twice, into two
	// namespaces, is TWO applications: they are separate copies with separate
	// lifecycles, and merging them would report one thing where an operator
	// has two to restart.
	Namespace NamespaceName
	// PartOf is app.kubernetes.io/part-of, empty when unset.
	PartOf string
	// Name is app.kubernetes.io/name, empty when unset.
	Name string
	// ManagedBy is app.kubernetes.io/managed-by, empty when unset.
	ManagedBy string
	// Version is app.kubernetes.io/version, empty when unset.
	Version string
	// Members are the kinds it is made of, largest first.
	Members []ApplicationMember
	// Objects is how many objects it holds in total.
	Objects int
}

// ApplicationObject is one object being grouped, reduced to what grouping
// needs. The adapter gathers these; this decides what they add up to.
type ApplicationObject struct {
	Kind      string
	Namespace NamespaceName
	Labels    map[string]string
}

// ApplicationInventory is every application found, and what was not.
type ApplicationInventory struct {
	Applications []Application
	// Unlabelled is how many objects carried no instance label.
	//
	// SHOWN, NOT SWALLOWED. It is the difference between "this cluster has
	// eleven applications" and "this cluster has eleven applications and four
	// hundred objects that do not say which one they belong to", and only the
	// second is true of most clusters.
	Unlabelled int
}

// NewApplicationInventory groups objects by their instance label.
//
// Keyed on instance AND namespace: the same instance name in two namespaces
// is two deployed copies, and an operator restarting one is not restarting
// the other.
//
// Ordered by name, because an application list is looked up rather than
// ranked — and within one, by how many of each kind, so the thing it is
// mostly made of leads.
func NewApplicationInventory(objects []ApplicationObject) ApplicationInventory {
	type key struct {
		instance  string
		namespace NamespaceName
	}

	found := make(map[key]*Application)
	counts := make(map[key]map[string]int)
	inventory := ApplicationInventory{}

	for _, object := range objects {
		instance := object.Labels[LabelInstance]
		if instance == "" {
			inventory.Unlabelled++
			continue
		}

		id := key{instance: instance, namespace: object.Namespace}
		application, known := found[id]
		if !known {
			application = &Application{
				Instance:  instance,
				Namespace: object.Namespace,
				PartOf:    object.Labels[LabelPartOf],
				Name:      object.Labels[LabelName],
				ManagedBy: object.Labels[LabelManagedBy],
				Version:   object.Labels[LabelVersion],
			}
			found[id] = application
			counts[id] = make(map[string]int)
		}

		// FIRST NON-EMPTY WINS for the descriptive labels. Objects of one
		// application can disagree — a Service labelled with a version its
		// Deployment has moved past — and picking the first thing seen is at
		// least a value something actually carries, where merging or
		// preferring by kind would invent an answer.
		fillIn(application, object.Labels)

		application.Objects++
		counts[id][object.Kind]++
	}

	for id, application := range found {
		for kind, count := range counts[id] {
			application.Members = append(application.Members, ApplicationMember{Kind: kind, Count: count})
		}
		slices.SortStableFunc(application.Members, func(a, b ApplicationMember) int {
			if byCount := cmp.Compare(b.Count, a.Count); byCount != 0 {
				return byCount
			}
			return cmp.Compare(a.Kind, b.Kind)
		})
		inventory.Applications = append(inventory.Applications, *application)
	}

	slices.SortStableFunc(inventory.Applications, func(a, b Application) int {
		if byName := cmp.Compare(a.Instance, b.Instance); byName != 0 {
			return byName
		}
		return cmp.Compare(a.Namespace, b.Namespace)
	})

	return inventory
}

func fillIn(application *Application, labels map[string]string) {
	if application.PartOf == "" {
		application.PartOf = labels[LabelPartOf]
	}
	if application.Name == "" {
		application.Name = labels[LabelName]
	}
	if application.ManagedBy == "" {
		application.ManagedBy = labels[LabelManagedBy]
	}
	if application.Version == "" {
		application.Version = labels[LabelVersion]
	}
}
