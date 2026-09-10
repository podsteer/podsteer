package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Listing Helm releases from the LABELS on their Secrets, and never from
// their contents.
//
// ALL THE HELM-FORMAT KNOWLEDGE IN PODSTEER IS IN THIS FILE. The label names,
// the storage type, the object-name shape and the timestamp encoding are
// storage concerns, so they live in the adapter exactly as the Helm storage
// format itself does; the domain receives values it can argue about and knows
// nothing about how Helm writes a Secret.
//
// THE READ GOES THROUGH THE METADATA CLIENT, the same way Adapter.APIWriters
// does and for the same reason: `k8s.io/client-go/metadata` lists
// PartialObjectMetadata only — names, labels, annotations, managedFields —
// so NOT ONE BYTE of a Secret's data crosses the wire. That is what makes a
// release list possible at all under ADR 3: a release payload is a base64'd
// gzip'd megabyte, and forty releases read in full to render a list of forty
// names is precisely the bulk Secret read Kubernetes' own good-practices page
// tells operators to alert on.
//
// WHAT IT DOES NOT BUY IS ALSO WORTH STATING, because it is easy to assume
// otherwise: the metadata client narrows the RESPONSE, not the VERB. To RBAC
// this is `list secrets` like any other, so an account without it is refused —
// which is the ordinary case for a real part of this feature's audience, and
// why a refusal is a first-class answer here rather than an error.

// The labels Helm's own storage driver writes on every release Secret.
//
// Set by `newSecretsObject` in Helm's `pkg/storage/driver/secrets.go`. They
// are LABELS, not annotations — an earlier draft of the decision record had
// the two timestamps as annotations and was simply wrong, which matters
// because a label selector can filter on one and cannot on the other.
const (
	// helmOwnerLabel is the primary selector: Helm sets `owner=helm` on every
	// release Secret it writes, and on nothing else.
	helmOwnerLabel = "owner"
	// helmOwnerValue is what that label holds.
	helmOwnerValue = "helm"
	// helmNameLabel is the RELEASE name. It is the only field that says what
	// a release is called — see the note on parsing the object name below.
	helmNameLabel = "name"
	// helmVersionLabel is the revision number, as a decimal string.
	helmVersionLabel = "version"
	// helmStatusLabel is the release status, in Helm's own vocabulary.
	helmStatusLabel = "status"
	// helmCreatedAtLabel is when the revision was written, as Unix seconds.
	helmCreatedAtLabel = "createdAt"
	// helmModifiedAtLabel is when it was last updated, as Unix seconds. Helm
	// adds it only on an update, so it is absent on most revisions.
	helmModifiedAtLabel = "modifiedAt"
)

// helmReleaseSecretType is the Secret type Helm 3 writes.
//
// Used as a FIELD selector alongside the label selector, and it is worth
// being exact about what that buys. It cuts what crosses the WIRE, by letting
// the API server drop Secrets of every other type before serialising them. It
// does NOT cut what the API server DOES: the server reads and — where
// encryption at rest is configured — DECRYPTS every Secret in scope, and only
// then applies the selector. On a large namespace with encryption on, that is
// the expensive half, and no selector a client can send avoids it. It is kept
// because fewer bytes is still better than more, and because it makes the
// listing exact rather than merely well-labelled; it is not kept under any
// illusion that it makes the read cheap for the cluster.
const helmReleaseSecretType = "helm.sh/release.v1"

// secretsGVR is the core Secret resource, listed through the metadata client.
//
// A FIXED GVR rather than a RESTMapper lookup, the same choice trivy.go makes
// and for the same reason: this reads exactly one kind, which every cluster
// serves at exactly this group/version, so a discovery round trip would buy
// nothing.
var secretsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

// helmPageSize bounds one page of the metadata list, mirroring
// upgradeWriterPageSize and the table-print path's own paging rather than
// asking for a whole namespace of Secrets in one response.
const helmPageSize = 500

// helmScanLimit caps how many release Secrets one listing reads altogether.
//
// EVERY REVISION IS ITS OWN SECRET, so a release with two hundred upgrades is
// two hundred objects. Helm's own default keeps ten, and a cluster that
// raised that will make this reachable — which is why hitting it is REPORTED
// as truncation on the listing rather than silently capping a list somebody
// is reading in order to decide something.
const helmScanLimit = 5000

// helmCacheTTL is how long one scope's listing stands.
//
// FIVE MINUTES, and the number follows from what this is protecting against
// rather than from what feels fresh. Releases change on deploy cadence, not
// on a ten-second one, so a poll-tick read would pay for an answer that has
// not moved — and worse, would write six `list secrets` lines a minute into
// the operator's audit log for as long as the page was left open, which is
// the Secrets doctrine's own signature with the bytes removed and the pattern
// intact. It sits beside upgradeCacheTTL, which is the cache this one is
// modelled on.
//
// DELIBERATELY NOT MODELLED ON readCache. That one's window is two seconds
// and exists to collapse the pile-up INSIDE one refresh tick; its own doc
// says on-demand reads never go through it. This is an on-demand read, and
// the page states its own age beside an explicit Refresh that bypasses the
// cache entirely — which is what keeps a five-minute answer honest.
const helmCacheTTL = 5 * time.Minute

// ListHelmReleases returns what Helm has stored in one namespace, or
// cluster-wide when the namespace selects every namespace.
//
// A REFUSAL IS CACHED WITH ITS ERROR, and this is the one place that rule
// differs from its neighbours. The metrics-backend and vulnerability caches
// both cache a 403 as an EMPTY result, because for them "nothing installed"
// and "not permitted" lead to the same empty pane. Here that collapse is
// exactly what ADR 6 forbids: `list secrets` is the permission this page's
// likeliest readers do not hold, a cluster with no Helm releases is a LISTED
// answer with zero rows, and an entry that read "no Helm here" when it meant
// "not permitted here" would be wrong in the direction that wastes somebody's
// afternoon. So the refusal is carried on the listing, cached as a refusal,
// and rendered as one.
//
// A TRANSPORT FAILURE IS NOT CACHED — the same discipline
// DiscoverMetricsBackend follows. A cluster that was merely unreachable comes
// back, and should be asked again when it does.
//
// refresh SKIPS THE HELD ANSWER for this one call and replaces it. Set by the
// page's Refresh control and by nothing else — it is what makes the "as of"
// time beside that control actionable rather than decorative, since without a
// bypass Refresh would re-show the same five-minute-old listing and look
// broken. A refusal is re-asked on an explicit Refresh too, which is
// deliberate: an operator who has just been granted the permission has asked
// for exactly that, once, by pressing something.
func (a *Adapter) ListHelmReleases(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	refresh bool,
) (domain.HelmListing, error) {
	if !refresh {
		if entry, ok := a.helm.get(id, namespace); ok {
			return entry.listing, nil
		}
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.HelmListing{}, err
	}

	// SCOPED TO ONE NAMESPACE WHEN ONE IS FILTERED, CLUSTER-WIDE OTHERWISE,
	// and never a fan-out. The metadata client's Resource() with no
	// .Namespace() call lists every namespace at once in ONE request; asking
	// namespace by namespace would be a request per namespace per page open,
	// which on a cluster with a hundred namespaces is the storm this feature
	// is not allowed to be.
	client := set.meta.Resource(secretsGVR)
	var lister interface {
		List(context.Context, metav1.ListOptions) (*metav1.PartialObjectMetadataList, error)
	} = client
	if !namespace.IsAll() {
		lister = client.Namespace(namespace.String())
	}

	op := fmt.Sprintf("listing Helm release secrets in %q of %q", namespace, id)

	var (
		revisions     []domain.HelmRevision
		truncated     bool
		continueToken string
		scanned       int
	)

	for scanned < helmScanLimit {
		remaining := int64(helmScanLimit - scanned)
		pageSize := remaining
		if pageSize > helmPageSize {
			pageSize = helmPageSize
		}

		list, err := lister.List(ctx, metav1.ListOptions{
			// The LABEL selector is primary: `owner=helm` is what Helm's own
			// storage driver queries on, and it is the field that makes this
			// a release listing rather than a Secret listing.
			LabelSelector: helmOwnerLabel + "=" + helmOwnerValue,
			// The FIELD selector narrows to the release type as well. See
			// helmReleaseSecretType: it saves wire bytes and saves the API
			// server no work at all, since the server reads and decrypts
			// every Secret in scope before filtering.
			FieldSelector: "type=" + helmReleaseSecretType,
			Limit:         pageSize,
			Continue:      continueToken,
			// NO ResourceVersion, AND ITS ABSENCE IS THE WHOLE PAGING LOOP.
			// Asking the watch cache for a LIMITED list makes the server drop
			// the limit and return every matching Secret in one response with
			// no Continue token — so helmPageSize and helmScanLimit were both
			// inert, this loop ran exactly once, and it read the lot. On the
			// one read in this package that makes the API server decrypt
			// every Secret in scope. See cachedResourceVersion.
		})
		if err != nil {
			wrapped := classify(op, err)
			if errors.Is(wrapped, ports.ErrForbidden) || errors.Is(wrapped, ports.ErrUnauthenticated) {
				listing := domain.HelmListing{
					Releases: []domain.HelmRelease{},
					Status:   domain.HelmForbidden,
					Refusal:  helmForbiddenRefusal(namespace),
					ListedAt: time.Now().Unix(),
					Driver:   domain.HelmSecretDriver,
				}
				a.helm.put(id, namespace, listing)
				return listing, nil
			}
			return domain.HelmListing{}, wrapped
		}

		items := list.Items
		// A page holding more items than the remaining budget is what makes
		// truncation observable against a fake client that ignores Limit and
		// Continue and answers with everything at once; a real server leaves
		// a non-empty Continue token instead, handled below.
		if int64(len(items)) > pageSize {
			truncated = true
			items = items[:pageSize]
		}

		for i := range items {
			scanned++
			revision, ok := helmRevisionFrom(&items[i])
			if !ok {
				continue
			}
			revisions = append(revisions, revision)
		}

		continueToken = list.Continue
		if continueToken == "" {
			break
		}
	}
	if continueToken != "" {
		truncated = true
	}

	listing := domain.HelmListing{
		Releases:  domain.GroupHelmRevisions(revisions),
		Status:    domain.HelmListed,
		Truncated: truncated,
		ListedAt:  time.Now().Unix(),
		Driver:    domain.HelmSecretDriver,
	}
	a.helm.put(id, namespace, listing)
	return listing, nil
}

// helmRevisionFrom reads one release Secret's labels into a domain revision.
//
// THE `name` LABEL IS THE RELEASE NAME, never the object's own name. Helm
// writes the object as `sh.helm.release.v1.<release>.v<n>`, and recovering a
// release name from that would mean splitting on a dot in a string whose
// middle segment is itself allowed to contain dots. The label is what Helm's
// storage driver queries on and is the only field that answers this reliably.
//
// A Secret with no `name` label is reported as unusable rather than guessed
// at; the caller drops it, and domain.GroupHelmRevisions refuses an unnamed
// revision a second time for the same reason.
func helmRevisionFrom(object *metav1.PartialObjectMetadata) (domain.HelmRevision, bool) {
	labels := object.GetLabels()
	name := labels[helmNameLabel]
	if name == "" {
		return domain.HelmRevision{}, false
	}

	return domain.HelmRevision{
		Namespace: domain.NamespaceName(object.GetNamespace()),
		Name:      name,
		Revision:  helmInt(labels[helmVersionLabel]),
		// VERBATIM. Not validated against Helm's own list of statuses and not
		// defaulted: a status none of the domain's constants names arrives as
		// itself and renders as itself, the same rule the operator panels
		// follow for a controller's own vocabulary.
		Status:     domain.HelmStatus(labels[helmStatusLabel]),
		CreatedAt:  helmTimestamp(labels[helmCreatedAtLabel]),
		ModifiedAt: helmTimestamp(labels[helmModifiedAtLabel]),
		SecretName: object.GetName(),
	}, true
}

// helmInt reads a decimal label, treating anything unparseable as zero.
func helmInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

// helmTimestamp reads one of Helm's two timestamp labels.
//
// Helm writes them as Unix SECONDS in a decimal string. An absent or
// unparseable label becomes ZERO and never the current time: a missing
// timestamp substituted with `now` would render as a release that happened a
// moment ago, which is a claim nothing checked. `modifiedAt` is absent on
// most revisions by design, so this is the ordinary path rather than an
// error path.
func helmTimestamp(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

// helmForbiddenRefusal is the sentence shown in place of a listing.
//
// It NAMES THE PERMISSION, because that is a fact about the cluster's API
// rather than about this screen, and because the person reading it is
// routinely somebody whose account is configured exactly as intended — the
// Secrets doctrine exists precisely because many engineers deliberately hold
// no Secret access. It must never read as an absence of releases.
func helmForbiddenRefusal(namespace domain.NamespaceName) string {
	scope := "in this cluster"
	if !namespace.IsAll() {
		scope = "in " + namespace.String()
	}
	return "Your account may not list Secrets " + scope + ", and Helm stores every release in one. " +
		"This says nothing about whether Helm is used here. Reading the list needs `list` on " +
		"secrets in that scope — PodSteer asks for metadata only, but the metadata API narrows " +
		"the response rather than the permission."
}

// helmCache holds one listing per cluster and namespace scope.
//
// Modelled on upgradeCache — a mutex, a map and a TTL — rather than on
// readCache, whose two-second window exists to collapse a refresh tick's own
// pile-up and whose doc says on-demand reads never go through it.
//
// Keyed by cluster AND namespace because the listing is scoped: a
// namespace-filtered tab and an all-namespaces one are different reads, and
// "all namespaces" is its own key rather than a merge of the others — it is
// one list call, and stitching per-namespace answers together would report a
// stale namespace beside a fresh one.
//
// A FORBIDDEN LISTING IS STORED HERE COMPLETE, refusal and all. That is the
// deliberate divergence from backendCache and vulnerabilityCache, which store
// a 403 as an empty answer: see ListHelmReleases.
type helmCache struct {
	mu      sync.Mutex
	entries map[string]helmEntry
}

type helmEntry struct {
	at      time.Time
	listing domain.HelmListing
}

// helmCacheKey composes the per-cluster, per-scope key. The separator is one
// domain.ClusterID is forbidden from containing (see NewClusterID), so two
// clusters cannot collide by naming.
func helmCacheKey(id domain.ClusterID, namespace domain.NamespaceName) string {
	return id.String() + "|" + namespace.String()
}

func (c *helmCache) get(id domain.ClusterID, namespace domain.NamespaceName) (helmEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[helmCacheKey(id, namespace)]
	if !ok || time.Since(entry.at) > helmCacheTTL {
		return helmEntry{}, false
	}
	return entry, true
}

func (c *helmCache) put(id domain.ClusterID, namespace domain.NamespaceName, listing domain.HelmListing) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]helmEntry)
	}
	c.entries[helmCacheKey(id, namespace)] = helmEntry{at: time.Now(), listing: listing}
}

// forget drops one cluster's cached listings.
//
// Wired into BOTH Adapter.Invalidate and Adapter.forgetReads. Invalidate is
// the usual per-cluster lifecycle every cache here follows, so a reconnect
// cannot be answered from the previous connection's reads. forgetReads is the
// interesting one: it already runs after every ManagementPort write, so a
// write PodSteer made — a delete, an apply, a rollback — drops this listing
// too and the page refreshes itself on the operator's next look. That is one
// of the three refresh events ADR 6 names, obtained without a new hook for
// anything to forget to call.
func (c *helmCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := id.String() + "|"
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}
