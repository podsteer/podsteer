package domain_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// certNow anchors every relative window in this file — "expires in 5 days"
// means nothing without a fixed "now" to be five days ahead of.
var certNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// --- Certificate generation, entirely in-process. No fixture ever goes
// stale because none exists: every certificate below is minted with
// crypto/x509 in the test that needs it. ---

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	return key
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	return key
}

func pemBlock(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func rsaKeyPEM(key *rsa.PrivateKey) []byte {
	return pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}

func ecKeyPEM(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling EC key: %v", err)
	}
	return pemBlock("EC PRIVATE KEY", der)
}

// baseTemplate is a minimal, otherwise-valid leaf template a test starts
// from and overrides the one or two fields it is about.
func baseTemplate(cn string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(12345),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    certNow.Add(-24 * time.Hour),
		NotAfter:     certNow.Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
}

// selfSigned mints template as its own issuer, signed by key — the shape a
// self-signed leaf, or a root CA, takes.
func selfSigned(t *testing.T, template *x509.Certificate, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating self-signed certificate: %v", err)
	}
	return pemBlock("CERTIFICATE", der)
}

// issuedBy mints leafTemplate signed by issuerTemplate/issuerKey, carrying
// leafKey's public key — a chain of two's leaf half.
func issuedBy(t *testing.T, leafTemplate, issuerTemplate *x509.Certificate, issuerKey *rsa.PrivateKey, leafKey *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, leafTemplate, issuerTemplate, &leafKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("creating issued certificate: %v", err)
	}
	return pemBlock("CERTIFICATE", der)
}

// --- ParseCertificateChain -------------------------------------------------

func TestParseCertificateChainReadsTheLeafAlone(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	template := baseTemplate("app.example.com")
	certPEM := selfSigned(t, template, key)

	chain, err := domain.ParseCertificateChain(certPEM, certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if chain.Leaf.Subject == "" || !strings.Contains(chain.Leaf.Subject, "app.example.com") {
		t.Errorf("Leaf.Subject = %q, want it to name app.example.com", chain.Leaf.Subject)
	}
	if len(chain.Intermediates) != 0 {
		t.Errorf("Intermediates = %d, want 0 for a lone leaf", len(chain.Intermediates))
	}
	if chain.KeyMatches != nil {
		t.Errorf("KeyMatches = %v, want nil — no key was ever handed to ParseCertificateChain", chain.KeyMatches)
	}
}

// TestParseCertificateChainPutsTheLeafFirst is the one assertion that PEM
// ORDERING, not certificate content, decides which entry becomes Leaf. Two
// files holding the exact same two certificates in opposite order must
// disagree about which one is the leaf, or the promise in the doc comment is
// false.
func TestParseCertificateChainPutsTheLeafFirst(t *testing.T) {
	t.Parallel()

	caKey := rsaKey(t)
	caTemplate := baseTemplate("root-ca")
	caTemplate.IsCA = true
	caTemplate.BasicConstraintsValid = true
	caTemplate.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	caCertPEM := selfSigned(t, caTemplate, caKey)

	leafKey := rsaKey(t)
	leafTemplate := baseTemplate("app.example.com")
	leafCertPEM := issuedBy(t, leafTemplate, caTemplate, caKey, leafKey)

	leafFirst := append(append([]byte{}, leafCertPEM...), caCertPEM...)
	chain, err := domain.ParseCertificateChain(leafFirst, certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if !strings.Contains(chain.Leaf.Subject, "app.example.com") {
		t.Errorf("leaf-first: Leaf.Subject = %q, want app.example.com", chain.Leaf.Subject)
	}
	if len(chain.Intermediates) != 1 || !strings.Contains(chain.Intermediates[0].Subject, "root-ca") {
		t.Errorf("leaf-first: Intermediates = %+v, want one entry naming root-ca", chain.Intermediates)
	}
	if chain.Intermediates[0].IsCA != true {
		t.Errorf("intermediate IsCA = %v, want true", chain.Intermediates[0].IsCA)
	}
	if chain.Leaf.IsCA {
		t.Errorf("leaf IsCA = true, want false")
	}

	caFirst := append(append([]byte{}, caCertPEM...), leafCertPEM...)
	reversed, err := domain.ParseCertificateChain(caFirst, certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if !strings.Contains(reversed.Leaf.Subject, "root-ca") {
		t.Errorf("ca-first: Leaf.Subject = %q, want root-ca — ordering is supposed to decide this", reversed.Leaf.Subject)
	}
}

func TestParseCertificateChainIgnoresANonCertificateBlock(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	template := baseTemplate("app.example.com")
	certPEM := selfSigned(t, template, key)

	// A stray key block ahead of the certificate, the way a hand-assembled
	// bundle sometimes carries one.
	withKey := append(append([]byte{}, rsaKeyPEM(key)...), certPEM...)

	chain, err := domain.ParseCertificateChain(withKey, certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if !strings.Contains(chain.Leaf.Subject, "app.example.com") {
		t.Errorf("Leaf.Subject = %q, the non-certificate block should have been skipped, not fatal", chain.Leaf.Subject)
	}
}

func TestParseCertificateChainRejectsDataWithNoCertificate(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)

	if _, err := domain.ParseCertificateChain(rsaKeyPEM(key), certNow); err == nil {
		t.Error("ParseCertificateChain() with only a key block, want an error")
	} else if !hasError(err, domain.ErrInvalidCertificate) {
		t.Errorf("error = %v, want it to wrap ErrInvalidCertificate", err)
	}

	if _, err := domain.ParseCertificateChain([]byte("not pem at all"), certNow); err == nil {
		t.Error("ParseCertificateChain() with garbage, want an error")
	}
}

// --- VerifyKeyMatch ---------------------------------------------------------

func TestVerifyKeyMatchAcceptsAGenuinePair(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	certPEM := selfSigned(t, baseTemplate("app.example.com"), key)

	matches, err := domain.VerifyKeyMatch(certPEM, rsaKeyPEM(key))
	if err != nil {
		t.Fatalf("VerifyKeyMatch() error = %v", err)
	}
	if !matches {
		t.Error("VerifyKeyMatch() = false, want true for the certificate's own key")
	}
}

func TestVerifyKeyMatchRejectsAWrongKey(t *testing.T) {
	t.Parallel()

	certKey := rsaKey(t)
	certPEM := selfSigned(t, baseTemplate("app.example.com"), certKey)

	// A second, unrelated key — the shape of a Secret assembled from two
	// different certificate requests.
	otherKey := rsaKey(t)

	matches, err := domain.VerifyKeyMatch(certPEM, rsaKeyPEM(otherKey))
	if err != nil {
		t.Fatalf("VerifyKeyMatch() error = %v", err)
	}
	if matches {
		t.Error("VerifyKeyMatch() = true, want false for a key that does not belong to this certificate")
	}
}

func TestVerifyKeyMatchAcceptsAnECDSAPair(t *testing.T) {
	t.Parallel()

	key := ecKey(t)
	der, err := x509.CreateCertificate(rand.Reader, baseTemplate("app.example.com"), baseTemplate("app.example.com"), &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	certPEM := pemBlock("CERTIFICATE", der)

	matches, err := domain.VerifyKeyMatch(certPEM, ecKeyPEM(t, key))
	if err != nil {
		t.Fatalf("VerifyKeyMatch() error = %v", err)
	}
	if !matches {
		t.Error("VerifyKeyMatch() = false, want true for the certificate's own EC key")
	}
}

func TestVerifyKeyMatchOnUnparsableKeyIsAnError(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	certPEM := selfSigned(t, baseTemplate("app.example.com"), key)

	if _, err := domain.VerifyKeyMatch(certPEM, []byte("not a key")); err == nil {
		t.Error("VerifyKeyMatch() with unparsable key data, want an error")
	}
}

// --- Certificate fields ------------------------------------------------------

func TestParseCertificateChainReadsEveryKindOfSAN(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	template := baseTemplate("app.example.com")
	template.DNSNames = []string{"app.example.com", "app2.example.com"}
	template.IPAddresses = []net.IP{net.ParseIP("10.0.0.1")}
	template.EmailAddresses = []string{"ops@example.com"}
	spiffe, err := url.Parse("spiffe://cluster.local/ns/default/sa/app")
	if err != nil {
		t.Fatalf("parsing URI: %v", err)
	}
	template.URIs = []*url.URL{spiffe}

	chain, err := domain.ParseCertificateChain(selfSigned(t, template, key), certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}

	want := []string{
		"app.example.com", "app2.example.com",
		"10.0.0.1",
		"ops@example.com",
		"spiffe://cluster.local/ns/default/sa/app",
	}
	if !equalStrings(chain.Leaf.SANs, want) {
		t.Errorf("SANs = %v, want %v", chain.Leaf.SANs, want)
	}
}

func TestParseCertificateChainReportsKeySize(t *testing.T) {
	t.Parallel()

	rsaCertPEM := selfSigned(t, baseTemplate("rsa.example.com"), rsaKey(t))
	rsaChain, err := domain.ParseCertificateChain(rsaCertPEM, certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if rsaChain.Leaf.KeyBits != 2048 {
		t.Errorf("RSA KeyBits = %d, want 2048", rsaChain.Leaf.KeyBits)
	}
	if rsaChain.Leaf.PublicKeyAlgorithm != "RSA" {
		t.Errorf("PublicKeyAlgorithm = %q, want RSA", rsaChain.Leaf.PublicKeyAlgorithm)
	}

	ecKeyValue := ecKey(t)
	der, err := x509.CreateCertificate(rand.Reader, baseTemplate("ec.example.com"), baseTemplate("ec.example.com"), &ecKeyValue.PublicKey, ecKeyValue)
	if err != nil {
		t.Fatalf("creating EC certificate: %v", err)
	}
	ecChain, err := domain.ParseCertificateChain(pemBlock("CERTIFICATE", der), certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if ecChain.Leaf.KeyBits != 256 {
		t.Errorf("EC KeyBits = %d, want 256 for P256", ecChain.Leaf.KeyBits)
	}
}

func TestParseCertificateChainDetectsSelfSignedBySignatureNotJustByName(t *testing.T) {
	t.Parallel()

	key := rsaKey(t)
	chain, err := domain.ParseCertificateChain(selfSigned(t, baseTemplate("app.example.com"), key), certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if !chain.Leaf.SelfSigned {
		t.Error("SelfSigned = false, want true for a certificate signed by its own key")
	}

	// A leaf issued by a genuine, separate CA is not self-signed, even
	// though nothing about parsing it looks different structurally.
	caKey := rsaKey(t)
	caTemplate := baseTemplate("root-ca")
	caTemplate.IsCA = true
	caTemplate.BasicConstraintsValid = true
	caTemplate.KeyUsage = x509.KeyUsageCertSign
	leafKey := rsaKey(t)
	issuedChain, err := domain.ParseCertificateChain(issuedBy(t, baseTemplate("app.example.com"), caTemplate, caKey, leafKey), certNow)
	if err != nil {
		t.Fatalf("ParseCertificateChain() error = %v", err)
	}
	if issuedChain.Leaf.SelfSigned {
		t.Error("SelfSigned = true, want false for a certificate issued by a separate CA")
	}
}

// --- ExpiresIn ---------------------------------------------------------------

func TestCertificateExpiresIn(t *testing.T) {
	t.Parallel()

	cert := domain.Certificate{NotAfter: certNow.Add(5 * 24 * time.Hour)}
	if got := cert.ExpiresIn(certNow); got != 5*24*time.Hour {
		t.Errorf("ExpiresIn() = %v, want 120h", got)
	}

	expired := domain.Certificate{NotAfter: certNow.Add(-1 * time.Hour)}
	if got := expired.ExpiresIn(certNow); got >= 0 {
		t.Errorf("ExpiresIn() = %v, want negative for an expired certificate", got)
	}
}

// --- CertificateFindings ------------------------------------------------------

func hasInsightTitled(insights []domain.CertificateInsight, substring string) (domain.CertificateInsight, bool) {
	for _, insight := range insights {
		if strings.Contains(insight.Title, substring) {
			return insight, true
		}
	}
	return domain.CertificateInsight{}, false
}

// aHealthyLeaf is what every "one thing wrong" test below starts from and
// perturbs — a certificate a correctly configured Secret would hold.
func aHealthyLeaf() domain.Certificate {
	return domain.Certificate{
		Subject:    "CN=app.example.com",
		Issuer:     "CN=Example CA",
		SANs:       []string{"app.example.com"},
		NotBefore:  certNow.Add(-24 * time.Hour),
		NotAfter:   certNow.Add(90 * 24 * time.Hour),
		SelfSigned: false,
	}
}

func TestCertificateFindingsStaysQuietOnAHealthyChain(t *testing.T) {
	t.Parallel()

	matches := true
	chain := domain.CertificateChain{Leaf: aHealthyLeaf(), KeyMatches: &matches}

	if got := domain.CertificateFindings(chain, certNow); len(got) != 0 {
		t.Errorf("CertificateFindings() = %+v, want none for a healthy chain", got)
	}
}

func TestCertificateFindingsFlagsAnExpiredLeaf(t *testing.T) {
	t.Parallel()

	leaf := aHealthyLeaf()
	leaf.NotAfter = certNow.Add(-1 * time.Hour)

	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: leaf}, certNow)
	insight, found := hasInsightTitled(insights, "expired")
	if !found {
		t.Fatalf("CertificateFindings() = %+v, want an expiry insight", insights)
	}
	if insight.Severity != domain.SeverityCritical {
		t.Errorf("expired severity = %v, want critical", insight.Severity)
	}
}

func TestCertificateFindingsWarnsBeforeExpiryButNotLongBefore(t *testing.T) {
	t.Parallel()

	soon := aHealthyLeaf()
	soon.NotAfter = certNow.Add(5 * 24 * time.Hour)
	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: soon}, certNow)
	insight, found := hasInsightTitled(insights, "expires soon")
	if !found {
		t.Fatalf("CertificateFindings() for a cert expiring in 5 days = %+v, want an expiry warning", insights)
	}
	if insight.Severity != domain.SeverityWarning {
		t.Errorf("severity = %v, want warning", insight.Severity)
	}

	comfortable := aHealthyLeaf()
	comfortable.NotAfter = certNow.Add(90 * 24 * time.Hour)
	if insights := domain.CertificateFindings(domain.CertificateChain{Leaf: comfortable}, certNow); len(insights) != 0 {
		t.Errorf("CertificateFindings() for a cert expiring in 90 days = %+v, want none", insights)
	}
}

func TestCertificateFindingsFlagsANotYetValidCertificate(t *testing.T) {
	t.Parallel()

	future := aHealthyLeaf()
	future.NotBefore = certNow.Add(24 * time.Hour)

	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: future}, certNow)
	insight, found := hasInsightTitled(insights, "not yet valid")
	if !found {
		t.Fatalf("CertificateFindings() = %+v, want a not-yet-valid insight", insights)
	}
	if insight.Severity != domain.SeverityWarning {
		t.Errorf("severity = %v, want warning", insight.Severity)
	}
}

func TestCertificateFindingsNotesASelfSignedLeafAtInfoOnly(t *testing.T) {
	t.Parallel()

	leaf := aHealthyLeaf()
	leaf.SelfSigned = true

	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: leaf}, certNow)
	insight, found := hasInsightTitled(insights, "self-signed")
	if !found {
		t.Fatalf("CertificateFindings() = %+v, want a self-signed insight", insights)
	}
	if insight.Severity != domain.SeverityInfo {
		t.Errorf("self-signed severity = %v, want info — it is routine, not a fault", insight.Severity)
	}
}

func TestCertificateFindingsFlagsAKeyThatDoesNotMatch(t *testing.T) {
	t.Parallel()

	mismatch := false
	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: aHealthyLeaf(), KeyMatches: &mismatch}, certNow)
	insight, found := hasInsightTitled(insights, "Key does not match")
	if !found {
		t.Fatalf("CertificateFindings() = %+v, want a key-mismatch insight", insights)
	}
	if insight.Severity != domain.SeverityCritical {
		t.Errorf("severity = %v, want critical — every handshake using this Secret will fail", insight.Severity)
	}
}

func TestCertificateFindingsLeavesKeyMatchAloneWhenNoKeyWasInspected(t *testing.T) {
	t.Parallel()

	// KeyMatches nil, not false: no tls.key was in the Secret at all, which
	// must read as "not checked", never as "checked and failed".
	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: aHealthyLeaf(), KeyMatches: nil}, certNow)
	if _, found := hasInsightTitled(insights, "does not match"); found {
		t.Errorf("CertificateFindings() = %+v, want no key-mismatch insight when KeyMatches is nil", insights)
	}
}

func TestCertificateFindingsFlagsAnEmptySANList(t *testing.T) {
	t.Parallel()

	leaf := aHealthyLeaf()
	leaf.SANs = nil

	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: leaf}, certNow)
	insight, found := hasInsightTitled(insights, "Subject Alternative Names")
	if !found {
		t.Fatalf("CertificateFindings() = %+v, want an empty-SAN insight", insights)
	}
	if insight.Severity != domain.SeverityWarning {
		t.Errorf("severity = %v, want warning", insight.Severity)
	}
}

func TestCertificateFindingsRanksCriticalAboveEverythingElse(t *testing.T) {
	t.Parallel()

	leaf := aHealthyLeaf()
	leaf.NotAfter = certNow.Add(-1 * time.Hour) // critical: expired
	leaf.SANs = nil                             // warning: no SANs
	leaf.SelfSigned = true                      // info: self-signed

	insights := domain.CertificateFindings(domain.CertificateChain{Leaf: leaf}, certNow)
	if len(insights) < 3 {
		t.Fatalf("CertificateFindings() = %+v, want at least 3 insights", insights)
	}
	if insights[0].Severity != domain.SeverityCritical {
		t.Errorf("insights[0].Severity = %v, want critical to lead", insights[0].Severity)
	}
	if insights[len(insights)-1].Severity != domain.SeverityInfo {
		t.Errorf("insights[last].Severity = %v, want info to trail", insights[len(insights)-1].Severity)
	}
}

// --- helpers ------------------------------------------------------------

func hasError(err error, target error) bool {
	return errors.Is(err, target)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
