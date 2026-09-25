package k8s

import (
	"context"
	"strings"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// warningCollector gathers the warning headers the API server attached to one
// apply.
//
// WHY THIS EXISTS AT ALL. The API server answers a write it has ACCEPTED with
// `Warning:` headers — "apps/v1beta1 Deployment is deprecated in v1.25+, use
// apps/v1", an admission webhook that chose to warn rather than reject, a
// field it does not recognise. That is the cluster telling the operator
// something at the exact moment they can act on it, and PodSteer used to drop
// it on the floor: ApplyOutcome.Warnings existed, was documented, was carried
// across the bridge, and was always empty.
//
// WHY IT IS PER APPLY AND NOT PER CLUSTER, which is the part that made this
// look harder than it is. client-go hangs the handler off the REST CONFIG, not
// off the request, and one config is shared by every dynamic call a cluster
// ever makes — so a collector installed there would attribute warnings to
// whichever apply happened to read the slice next. Two tabs on the same
// context are enough to get it wrong.
//
// The answer is a config of this apply's own: rest.CopyConfig is cheap, and
// client-go caches transports by their TLS options rather than by the whole
// config, so a per-apply client shares the connection pool with every other
// client for that cluster. The cost is one struct copy on an ATTENDED WRITE —
// not a poll, not a list, once per button press.
type warningCollector struct {
	mu       sync.Mutex
	warnings []string
}

// HandleWarningHeader implements rest.WarningHandler.
//
// The agent and the RFC 7234 warn-code are deliberately dropped. The code is
// always 299 in practice and the agent is the API server's own name; neither
// tells an operator anything the text does not, and prefixing every line with
// "299 - " would make the panel read like a log file.
func (c *warningCollector) HandleWarningHeader(_ int, _ string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// DEDUPLICATED, because one apply can produce the same warning several
	// times: a dry run followed by the real write is two requests, and a
	// server that warns about a deprecated apiVersion warns about it on both.
	// An operator reading "this is deprecated" twice would reasonably wonder
	// what the second one is about.
	for _, seen := range c.warnings {
		if seen == text {
			return
		}
	}
	c.warnings = append(c.warnings, text)
}

// HandleWarningHeaderWithContext implements rest.WarningHandlerWithContext.
//
// client-go prefers this one when both are set. It carries a context for
// contextual logging, which this collector has no use for — but implementing
// only the older interface would leave the newer one to fall through to the
// package-level default handler, which logs rather than collects.
func (c *warningCollector) HandleWarningHeaderWithContext(_ context.Context, code int, agent string, text string) {
	c.HandleWarningHeader(code, agent, text)
}

// collected returns what the server warned about, oldest first.
func (c *warningCollector) collected() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.warnings) == 0 {
		// Nil rather than an empty slice: ApplyOutcome.Warnings documents
		// empty as the common case and not worth reporting as an absence, and
		// the DTO turns nil into [] for the wire.
		return nil
	}
	return append([]string(nil), c.warnings...)
}

// applyClient builds a dynamic client whose warnings land in the collector.
//
// The two content-type lines mirror clientsFor: the dynamic client speaks JSON
// only, because protobuf has no representation for unstructured objects. They
// are repeated rather than inherited because CopyConfig copies the BASE config
// — the one the typed client uses — not the dynamic variant built from it.
func applyClient(set *clients) (dynamic.Interface, *warningCollector, error) {
	collector := &warningCollector{}

	// A CLIENT SET WITH NO REST CONFIG CANNOT COLLECT WARNINGS, and rather
	// than nil-panic on CopyConfig it says so by collecting none. Every set
	// clientsFor builds carries a config; the ones that do not are assembled
	// by hand in tests around a fake dynamic client, which never emits a
	// warning header for there to be anything to collect. The shared client
	// is returned so the apply itself still works.
	if set.config == nil {
		return set.dynamic, collector, nil
	}

	config := rest.CopyConfig(set.config)
	config.ContentType = "application/json"
	config.AcceptContentTypes = "application/json"
	config.WarningHandler = collector
	config.WarningHandlerWithContext = collector

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}
	return client, collector, nil
}
