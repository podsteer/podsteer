package wails

import (
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

var dtoNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// TestToPodCarriesEachContainersTTYAndStdin pins the wire contract the
// terminal pane's Attach control depends on: whether a container has a tty
// and keeps stdin open must reach the frontend exactly as the domain carries
// it, per container, not collapsed to the pod as a whole.
func TestToPodCarriesEachContainersTTYAndStdin(t *testing.T) {
	t.Parallel()

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      "web-0",
		Namespace: "default",
		ClusterID: "dev",
		Containers: []domain.Container{
			{Name: "app", TTY: true, Stdin: true},
			{Name: "sidecar"},
		},
	})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	dto := toPod(pod, dtoNow)

	if len(dto.Containers) != 2 {
		t.Fatalf("Containers = %d, want 2", len(dto.Containers))
	}
	if !dto.Containers[0].TTY || !dto.Containers[0].Stdin {
		t.Errorf("Containers[0] TTY/Stdin = %v/%v, want true/true", dto.Containers[0].TTY, dto.Containers[0].Stdin)
	}
	if dto.Containers[1].TTY || dto.Containers[1].Stdin {
		t.Errorf("Containers[1] TTY/Stdin = %v/%v, want false/false", dto.Containers[1].TTY, dto.Containers[1].Stdin)
	}
}

func TestToCertificateFormatsTimesAsRFC3339AndSizesExpiry(t *testing.T) {
	t.Parallel()

	cert := domain.Certificate{
		Subject:            "CN=app.example.com",
		Issuer:             "CN=Example CA",
		SANs:               []string{"app.example.com"},
		NotBefore:          dtoNow.Add(-24 * time.Hour),
		NotAfter:           dtoNow.Add(5 * 24 * time.Hour),
		SerialNumber:       "12345",
		SignatureAlgorithm: "SHA256-RSA",
		PublicKeyAlgorithm: "RSA",
		KeyBits:            2048,
		IsCA:               false,
		SelfSigned:         false,
	}

	got := toCertificate(cert, dtoNow)

	if got.NotBefore != cert.NotBefore.UTC().Format(time.RFC3339) {
		t.Errorf("NotBefore = %q, want RFC 3339", got.NotBefore)
	}
	if got.NotAfter != cert.NotAfter.UTC().Format(time.RFC3339) {
		t.Errorf("NotAfter = %q, want RFC 3339", got.NotAfter)
	}
	if got.ExpiresInSeconds != int64(5*24*time.Hour/time.Second) {
		t.Errorf("ExpiresInSeconds = %d, want %d", got.ExpiresInSeconds, int64(5*24*time.Hour/time.Second))
	}
	if got.Subject != cert.Subject || got.Issuer != cert.Issuer {
		t.Errorf("Subject/Issuer = %q/%q, want %q/%q", got.Subject, got.Issuer, cert.Subject, cert.Issuer)
	}
	if got.KeyBits != 2048 {
		t.Errorf("KeyBits = %d, want 2048", got.KeyBits)
	}
}

func TestToCertificateExpiresInSecondsGoesNegativeAfterExpiry(t *testing.T) {
	t.Parallel()

	cert := domain.Certificate{NotAfter: dtoNow.Add(-2 * time.Hour)}
	got := toCertificate(cert, dtoNow)

	if got.ExpiresInSeconds >= 0 {
		t.Errorf("ExpiresInSeconds = %d, want negative for an expired certificate", got.ExpiresInSeconds)
	}
}

func TestToCertificateNeverSendsANilSANList(t *testing.T) {
	t.Parallel()

	got := toCertificate(domain.Certificate{SANs: nil}, dtoNow)
	if got.SANs == nil {
		t.Error("SANs = nil, want [] so the frontend never needs a null check")
	}
}

func TestToCertificateChainCarriesKeyMatchesThroughIncludingNil(t *testing.T) {
	t.Parallel()

	matches := true
	withKey := toCertificateChain(domain.CertificateChain{
		Leaf:       domain.Certificate{NotAfter: dtoNow.Add(90 * 24 * time.Hour)},
		KeyMatches: &matches,
	}, dtoNow)
	if withKey.KeyMatches == nil || !*withKey.KeyMatches {
		t.Errorf("KeyMatches = %v, want a pointer to true", withKey.KeyMatches)
	}

	withoutKey := toCertificateChain(domain.CertificateChain{
		Leaf: domain.Certificate{NotAfter: dtoNow.Add(90 * 24 * time.Hour)},
	}, dtoNow)
	if withoutKey.KeyMatches != nil {
		t.Errorf("KeyMatches = %v, want nil — no key was inspected, which must not read as false", withoutKey.KeyMatches)
	}
}

func TestToCertificateChainCarriesIntermediatesAndInsights(t *testing.T) {
	t.Parallel()

	chain := domain.CertificateChain{
		Leaf: domain.Certificate{
			Subject:   "CN=app.example.com",
			NotAfter:  dtoNow.Add(-time.Hour), // expired, so an insight is guaranteed
			NotBefore: dtoNow.Add(-48 * time.Hour),
			SANs:      []string{"app.example.com"},
		},
		Intermediates: []domain.Certificate{
			{Subject: "CN=Intermediate CA", IsCA: true},
		},
	}

	got := toCertificateChain(chain, dtoNow)

	if len(got.Intermediates) != 1 || got.Intermediates[0].Subject != "CN=Intermediate CA" {
		t.Errorf("Intermediates = %+v, want one entry naming the intermediate CA", got.Intermediates)
	}
	if len(got.Insights) == 0 {
		t.Fatal("Insights is empty, want at least the expiry insight for an expired leaf")
	}
	found := false
	for _, insight := range got.Insights {
		if insight.Severity == string(domain.SeverityCritical) {
			found = true
		}
	}
	if !found {
		t.Errorf("Insights = %+v, want a critical severity for an expired leaf", got.Insights)
	}
}

// TestRowsNeverSendNullLabelsOrAnnotations pins the shape every list view
// indexes without a guard: a row with no metadata arrives as {} on both
// fields, never as null, for every kind that carries them.
func TestRowsNeverSendNullLabelsOrAnnotations(t *testing.T) {
	t.Parallel()

	pod, err := domain.NewPod(domain.PodSpec{Name: "web-0", Namespace: "default", ClusterID: "dev"})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	node, err := domain.NewNode(domain.NodeSpec{Name: "node-1", ClusterID: "dev"})
	if err != nil {
		t.Fatalf("NewNode() error = %v", err)
	}
	namespace, err := domain.NewNamespace("default", domain.NamespacePhaseActive, dtoNow)
	if err != nil {
		t.Fatalf("NewNamespace() error = %v", err)
	}
	event, err := domain.NewEvent(domain.EventSpec{Name: "web-0.1", Namespace: "default", ClusterID: "dev"})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	for name, pair := range map[string][2]map[string]string{
		"pod":       {toPod(pod, dtoNow).Labels, toPod(pod, dtoNow).Annotations},
		"node":      {toNode(node, dtoNow).Labels, toNode(node, dtoNow).Annotations},
		"namespace": {toNamespace(namespace, dtoNow).Labels, toNamespace(namespace, dtoNow).Annotations},
		"event":     {toEvent(event, dtoNow).Labels, toEvent(event, dtoNow).Annotations},
	} {
		if pair[0] == nil || pair[1] == nil {
			t.Errorf("%s: labels/annotations = %v/%v, want {} for both", name, pair[0], pair[1])
		}
	}
}

// TestToResourceTableCarriesRowLabelsAndProjectedAnnotations pins that the
// generic path ships what the adapter read from each row's own metadata —
// which is what lets a CRD grow a custom column with no code written for it.
func TestToResourceTableCarriesRowLabelsAndProjectedAnnotations(t *testing.T) {
	t.Parallel()

	kind := domain.ResourceKind{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true, Title: "Config Maps"}
	table := domain.NewResourceTable(kind,
		[]domain.TableColumn{{Name: "Name", Type: "string"}},
		[]domain.TableRow{
			{
				Name:        "app-config",
				Namespace:   "platform",
				Cells:       []string{"app-config"},
				Labels:      map[string]string{"app": "web"},
				Annotations: map[string]string{"team": "payments"},
			},
			{Name: "bare", Namespace: "platform", Cells: []string{"bare"}},
		},
	)

	dto := toResourceTable(table)
	if len(dto.Rows) != 2 {
		t.Fatalf("Rows = %d, want 2", len(dto.Rows))
	}
	if dto.Rows[0].Labels["app"] != "web" || dto.Rows[0].Annotations["team"] != "payments" {
		t.Errorf("Rows[0] labels/annotations = %v/%v, want app=web / team=payments",
			dto.Rows[0].Labels, dto.Rows[0].Annotations)
	}
	if dto.Rows[1].Labels == nil || dto.Rows[1].Annotations == nil {
		t.Errorf("Rows[1] labels/annotations = %v/%v, want {} for both", dto.Rows[1].Labels, dto.Rows[1].Annotations)
	}
}
