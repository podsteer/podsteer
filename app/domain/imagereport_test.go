package domain_test

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestParseImageReferenceAppliesTheDefaultsARuntimeWouldApply(t *testing.T) {
	tests := []struct {
		name           string
		ref            string
		wantRegistry   string
		wantRepository string
		wantTag        string
		wantDigest     string
	}{
		{
			name: "a bare name is Docker Hub's library, tagged latest", ref: "nginx",
			wantRegistry: "docker.io", wantRepository: "library/nginx", wantTag: "latest",
		},
		{
			name: "a two-segment name on Docker Hub keeps its own namespace", ref: "bitnami/postgresql:16",
			wantRegistry: "docker.io", wantRepository: "bitnami/postgresql", wantTag: "16",
		},
		{
			name: "a host with a dot is a registry rather than a path segment", ref: "ghcr.io/team/app:v1.2.3",
			wantRegistry: "ghcr.io", wantRepository: "team/app", wantTag: "v1.2.3",
		},
		{
			name: "a host with a port is a registry, and the port is not a tag", ref: "registry.example.com:5000/team/app",
			wantRegistry: "registry.example.com:5000", wantRepository: "team/app", wantTag: "latest",
		},
		{
			name: "localhost is a registry without needing a dot", ref: "localhost:5000/app:dev",
			wantRegistry: "localhost:5000", wantRepository: "app", wantTag: "dev",
		},
		{
			name:         "a digest-only reference has no tag defaulted onto it",
			ref:          "ghcr.io/team/app@sha256:0000000000000000000000000000000000000000000000000000000000000001",
			wantRegistry: "ghcr.io", wantRepository: "team/app", wantTag: "",
			wantDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000001",
		},
		{
			name:         "a tag and a digest together keep both",
			ref:          "ghcr.io/team/app:v1@sha256:0000000000000000000000000000000000000000000000000000000000000002",
			wantRegistry: "ghcr.io", wantRepository: "team/app", wantTag: "v1",
			wantDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000002",
		},
		{
			name:         "the runtime prefix some kubelets still write on an imageID is not part of the reference",
			ref:          "docker-pullable://nginx@sha256:0000000000000000000000000000000000000000000000000000000000000003",
			wantRegistry: "docker.io", wantRepository: "library/nginx",
			wantDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := domain.ParseImageReference(tt.ref)
			if !ok {
				t.Fatalf("ParseImageReference(%q) refused a valid reference", tt.ref)
			}
			if got.Registry != tt.wantRegistry {
				t.Errorf("registry = %q, want %q", got.Registry, tt.wantRegistry)
			}
			if got.Repository != tt.wantRepository {
				t.Errorf("repository = %q, want %q", got.Repository, tt.wantRepository)
			}
			if got.Tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", got.Tag, tt.wantTag)
			}
			if got.Digest != tt.wantDigest {
				t.Errorf("digest = %q, want %q", got.Digest, tt.wantDigest)
			}
		})
	}
}

// A malformed digest is a refusal, never a reference that quietly lost its
// digest: the commonest shape of one is a truncated copy out of a log, and
// treating it as "no digest" turns a pinned reference into a tagged one in
// whatever compares it next.
func TestParseImageReferenceRefusesAMalformedReference(t *testing.T) {
	for _, ref := range []string{
		"",
		"nginx@sha256:abc",
		"nginx@sha256:" + strings.Repeat("g", 64),
		"nginx@notadigest",
		"nginx:",
		"nginx: v1",
		"NGINX",
		"ghcr.io/team/app:",
	} {
		if _, ok := domain.ParseImageReference(ref); ok {
			t.Errorf("ParseImageReference(%q) accepted a malformed reference", ref)
		}
	}
}

const (
	indexDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	platformDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func TestNewImageReportReadsSizeFromTheNodeThatPulledTheImage(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		DeclaredRef: "ghcr.io/team/app:v1",
		ResolvedRef: "ghcr.io/team/app:v1",
		ImageID:     "ghcr.io/team/app@" + platformDigest,
		PullPolicy:  "IfNotPresent",
		NodeName:    "node-1",
		NodeImages: []domain.NodeImage{
			{Names: []string{"ghcr.io/team/app@" + platformDigest, "ghcr.io/team/app:v1"}, SizeBytes: 41_000_000},
			{Names: []string{"docker.io/library/nginx:1.27"}, SizeBytes: 60_000_000},
		},
	})

	if report.SizeStatus != domain.ImageSizeMeasured {
		t.Fatalf("size status = %q, want measured", report.SizeStatus)
	}
	if report.SizeBytes != 41_000_000 {
		t.Errorf("size = %d, want the entry matching this image", report.SizeBytes)
	}
	if !strings.Contains(report.SizeSource, "node-1") {
		t.Errorf("size source = %q, want it to name whose number it is", report.SizeSource)
	}
	if report.Digest != platformDigest {
		t.Errorf("digest = %q, want the one the kubelet recorded", report.Digest)
	}
	if report.Drift {
		t.Error("declared and resolved are the same reference; that is not drift")
	}
	if report.Bounded == "" {
		t.Fatal("every report has to say what it did not look at")
	}
}

// THE MULTI-PLATFORM CASE. An index and a platform manifest have different
// digests, and a node routinely records one while the container status
// records the other. Matching on the digest alone would report "the node does
// not list this image" for an image the node is plainly running.
func TestNewImageReportFallsBackToTheRepositoryWhenTheDigestsDiffer(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		ResolvedRef: "ghcr.io/team/app:v1",
		ImageID:     "ghcr.io/team/app@" + platformDigest,
		NodeName:    "node-1",
		NodeImages: []domain.NodeImage{
			{Names: []string{"ghcr.io/team/app@" + indexDigest, "ghcr.io/team/app:v1"}, SizeBytes: 41_000_000},
		},
	})

	if report.SizeStatus != domain.ImageSizeMeasured {
		t.Fatalf("size status = %q, want measured — the tag matched even though the digests did not", report.SizeStatus)
	}
	if report.DigestNote == "" {
		t.Fatal("a disagreement about the digest has to be stated, not hidden")
	}
	if !strings.Contains(report.DigestNote, indexDigest) || !strings.Contains(report.DigestNote, platformDigest) {
		t.Errorf("the note should carry both digests, got %q", report.DigestNote)
	}
	if !strings.Contains(report.DigestNote, "multi-platform") {
		t.Errorf("the note should offer the usual explanation, got %q", report.DigestNote)
	}
}

// A single-manifest image records one digest in both places, and there is
// nothing to explain — a note on every image would be a note nobody reads.
func TestNewImageReportSaysNothingWhenTheDigestsAgree(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		ResolvedRef: "ghcr.io/team/app:v1",
		ImageID:     "ghcr.io/team/app@" + platformDigest,
		NodeName:    "node-1",
		NodeImages: []domain.NodeImage{
			{Names: []string{"ghcr.io/team/app@" + platformDigest, "ghcr.io/team/app:v1"}, SizeBytes: 1},
		},
	})

	if report.DigestNote != "" {
		t.Errorf("digest note = %q, want none", report.DigestNote)
	}
}

func TestNewImageReportSeparatesTheThreeReasonsThereIsNoSize(t *testing.T) {
	tests := []struct {
		name       string
		facts      domain.ImageFacts
		wantStatus domain.ImageSizeStatus
		wantSaying string
	}{
		{
			name: "an unscheduled pod has not pulled anything",
			facts: domain.ImageFacts{
				Container: "web", DeclaredRef: "ghcr.io/team/app:v1",
			},
			wantStatus: domain.ImageSizeUnreadable,
			wantSaying: "not been scheduled",
		},
		{
			name: "a node the account may not read is unreadable, never zero",
			facts: domain.ImageFacts{
				Container: "web", ResolvedRef: "ghcr.io/team/app:v1",
				NodeName: "node-1", NodeUnreadable: "node node-1 could not be read",
			},
			wantStatus: domain.ImageSizeUnreadable,
			wantSaying: "could not be read",
		},
		{
			name: "a node that answered and does not list the image is an ordinary absence",
			facts: domain.ImageFacts{
				Container: "web", ResolvedRef: "ghcr.io/team/app:v1",
				NodeName:   "node-1",
				NodeImages: []domain.NodeImage{{Names: []string{"docker.io/library/nginx:1.27"}, SizeBytes: 60}},
			},
			wantStatus: domain.ImageSizeNotReported,
			wantSaying: "garbage-collects",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := domain.NewImageReport(tt.facts)
			if report.SizeStatus != tt.wantStatus {
				t.Errorf("size status = %q, want %q", report.SizeStatus, tt.wantStatus)
			}
			if report.SizeBytes != 0 {
				t.Errorf("size = %d, want zero — anything but measured must render as a dash", report.SizeBytes)
			}
			if !strings.Contains(report.SizeSource, tt.wantSaying) {
				t.Errorf("size source = %q, want it to contain %q", report.SizeSource, tt.wantSaying)
			}
		})
	}
}

// A moved tag shows up as one image on the node carrying two names, which is
// frequently the most useful thing on this pane.
func TestNewImageReportListsTheOtherNamesTheNodeKnows(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		ResolvedRef: "ghcr.io/team/app:v1",
		ImageID:     "ghcr.io/team/app@" + platformDigest,
		NodeName:    "node-1",
		NodeImages: []domain.NodeImage{{
			Names: []string{
				"ghcr.io/team/app:v1",
				"ghcr.io/team/app@" + platformDigest,
				"ghcr.io/team/app:stable",
			},
			SizeBytes: 7,
		}},
	})

	if len(report.OtherNames) != 2 {
		t.Fatalf("other names = %v, want the two that are not already on screen", report.OtherNames)
	}
	// Sorted, so the list does not reshuffle between one look and the next.
	if report.OtherNames[0] > report.OtherNames[1] {
		t.Errorf("other names = %v, want them sorted", report.OtherNames)
	}
	for _, name := range report.OtherNames {
		if name == report.Resolved {
			t.Error("the reference already shown must not repeat in the list")
		}
	}
}

func TestNewImageReportReportsDriftBetweenWhatWasAskedForAndWhatIsRunning(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		DeclaredRef: "ghcr.io/team/app:v1",
		ResolvedRef: "ghcr.io/team/app:v2",
	})

	if !report.Drift {
		t.Fatal("a declared reference differing from the resolved one is drift")
	}
	// The RESOLVED reference is the subject: what the kubelet says it is
	// running is a fact, and what the manifest asks for is an intention.
	if report.Reference.Tag != "v2" {
		t.Errorf("tag = %q, want the running one", report.Reference.Tag)
	}
}

// The pull Secret is NAMED and never read. That is what the pane says, and it
// is the honest answer to "this image needs credentials".
func TestNewImageReportNamesAPullSecretAndReadsNothingFromIt(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		ResolvedRef: "ghcr.io/team/private:v1",
		PullSecrets: []string{"ghcr-pull"},
	})

	if !report.Credentialed {
		t.Fatal("a pod naming an imagePullSecret is pulling with credentials")
	}
	if len(report.PullSecrets) != 1 || report.PullSecrets[0] != "ghcr-pull" {
		t.Errorf("pull secrets = %v, want the name", report.PullSecrets)
	}
	if !strings.Contains(domain.ImageCredentialNote, "does not read that Secret") {
		t.Errorf("the credential note has to state what was not read, got %q", domain.ImageCredentialNote)
	}
}

// The bounded line is not optional and is not a placeholder: empty space
// where layers would be is a claim nothing checked.
func TestEveryImageReportCarriesTheBoundedLine(t *testing.T) {
	for _, facts := range []domain.ImageFacts{
		{},
		{Container: "web", ResolvedRef: "nginx"},
		{Container: "web", ResolvedRef: "not a reference at all"},
	} {
		report := domain.NewImageReport(facts)
		if report.Bounded != domain.ImageDetailBounded {
			t.Errorf("bounded = %q, want the one line every report carries", report.Bounded)
		}
	}
	for _, word := range []string{"Layers", "registry", "kubeconfig"} {
		if !strings.Contains(domain.ImageDetailBounded, word) {
			t.Errorf("the bounded line should mention %q: %q", word, domain.ImageDetailBounded)
		}
	}
}

// A reference nothing can parse leaves the report readable rather than empty:
// the raw strings are still shown, and ReferenceReadable says the parse
// failed rather than the pane pretending the registry is unknown.
func TestNewImageReportSurvivesAReferenceItCannotParse(t *testing.T) {
	report := domain.NewImageReport(domain.ImageFacts{
		Container:   "web",
		DeclaredRef: "not a reference",
		ResolvedRef: "not a reference",
	})

	if report.ReferenceReadable {
		t.Fatal("an unparseable reference must not report itself as readable")
	}
	if report.Resolved != "not a reference" {
		t.Errorf("resolved = %q, want the raw string kept", report.Resolved)
	}
	if report.Reference.Registry != "" {
		t.Errorf("registry = %q, want nothing invented", report.Reference.Registry)
	}
}
