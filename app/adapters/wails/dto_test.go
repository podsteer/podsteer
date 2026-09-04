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
