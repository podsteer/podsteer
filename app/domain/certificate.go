package domain

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"time"
)

// This file is the certificate equivalent of pod_assessment.go: parsing and
// judgement, both pure, both argued with in a test rather than only observed
// against a real cluster. STANDARD LIBRARY ONLY — see CLAUDE.md's rule that
// app/domain imports nothing else — which is what makes a chain of test
// certificates generated with crypto/x509 itself a complete fixture, with no
// file on disk to go stale.
//
// Nothing here reaches a cluster or a clock of its own. A Secret is fetched
// exactly once, by the adapter, on the same deliberate request RevealSecretKey
// answers — see app/adapters/k8s/tls.go — and everything below is handed the
// bytes and a "now" it did not choose.

// certExpiryWarning is how far ahead of expiry a certificate is flagged.
//
// 14 days, not 30: a shorter window than most expiry monitors use, because
// this is read on request rather than watched continuously — the operator is
// already looking at this Secret, and a warning that fires a month out would
// repeat itself on every visit for weeks before it means anything.
const certExpiryWarning = 14 * 24 * time.Hour

// Certificate is one X.509 certificate as PodSteer displays it.
//
// A DISPLAY VALUE, NOT A KEY HOLDER. Nothing here can be turned back into
// key material — there is no public key field — because the only thing this
// type exists to do is cross into the DTO layer and out to the frontend, and
// what cannot be carried cannot leak.
type Certificate struct {
	Subject string
	Issuer  string
	// SANs are the Subject Alternative Names, in the order the standard
	// library exposes them: DNS names verbatim, then IP addresses in their
	// string form, then email addresses verbatim, then URIs by their own
	// String(). Empty when the certificate declares none — worth saying
	// itself, see CertificateFindings.
	SANs               []string
	NotBefore          time.Time
	NotAfter           time.Time
	SerialNumber       string
	SignatureAlgorithm string
	PublicKeyAlgorithm string
	// KeyBits is the modulus size for RSA, the curve's bit size for ECDSA,
	// and 0 for a key whose algorithm has no such notion (Ed25519) or that
	// this build does not recognise.
	KeyBits int
	IsCA    bool
	// SelfSigned is true only when the certificate's own signature verifies
	// against its own public key. A Subject equal to the Issuer is necessary
	// but not sufficient — two different authorities can share a name by
	// mistake or by policy — so the signature is what is actually checked.
	SelfSigned bool
}

// ExpiresIn reports how long remains until the certificate's NotAfter,
// relative to now. Negative once it has expired.
func (c Certificate) ExpiresIn(now time.Time) time.Duration {
	return c.NotAfter.Sub(now)
}

// CertificateChain is what one TLS Secret's certificate material held: the
// leaf a connection actually presents, and whatever else came with it.
type CertificateChain struct {
	Leaf          Certificate
	Intermediates []Certificate
	// KeyMatches reports whether a Secret's tls.key was found to match the
	// leaf's public key. Nil when no key was inspected at all — a Secret
	// carrying only ca.crt, say — which is a different fact from false and
	// must not be collapsed into it: nil means "not checked", false means
	// "checked, and it does not".
	KeyMatches *bool
}

// ParseCertificateChain decodes PEM-encoded certificate data into a chain.
//
// LEAF FIRST, AS PEM ORDERING REQUIRES: a well-formed tls.crt places the
// certificate a connection actually presents before whatever issued it, and
// that is the same order tls.X509KeyPair itself assumes. Every block in data
// is examined; a block that is not "CERTIFICATE" is skipped rather than
// failing the call, because a bundle occasionally carries a stray comment or
// a block some other tool appended.
//
// now is accepted here, alongside the parse, so that a chain freshly parsed
// is immediately ready for ExpiresIn and CertificateFindings below without a
// second clock reading elsewhere — parsing itself does not consult it.
func ParseCertificateChain(data []byte, now time.Time) (CertificateChain, error) {
	certs, err := parsePEMCertificates(data)
	if err != nil {
		return CertificateChain{}, fmt.Errorf("%w: %w", ErrInvalidCertificate, err)
	}
	if len(certs) == 0 {
		return CertificateChain{}, fmt.Errorf("%w: no certificate found", ErrInvalidCertificate)
	}

	chain := CertificateChain{
		Leaf:          mapCertificate(certs[0]),
		Intermediates: make([]Certificate, 0, len(certs)-1),
	}
	for _, cert := range certs[1:] {
		chain.Intermediates = append(chain.Intermediates, mapCertificate(cert))
	}

	return chain, nil
}

// VerifyKeyMatch reports whether keyPEM's private key is the pair of the
// LEAF certificate's public key in certPEM — the leaf being first, by the
// same PEM ordering ParseCertificateChain reads.
//
// AN X.509 KEY COMPARISON, NOTHING MORE. Both PEM blocks are decoded only far
// enough to compare their public keys, and neither the certificate nor the
// key is retained past this call or reachable from the bool it returns — the
// same discipline RevealSecretKey documents for a Secret's other values.
func VerifyKeyMatch(certPEM, keyPEM []byte) (bool, error) {
	certs, err := parsePEMCertificates(certPEM)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrInvalidCertificate, err)
	}
	if len(certs) == 0 {
		return false, fmt.Errorf("%w: no certificate found", ErrInvalidCertificate)
	}

	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return false, fmt.Errorf("parsing private key: %w", err)
	}

	return publicKeysEqual(certs[0].PublicKey, key.Public()), nil
}

// CertificateInsight is one thing worth telling an operator about an
// inspected certificate chain.
//
// THE SAME SHAPE AS PodFinding, AND DELIBERATELY NOT THE SAME TYPE — for the
// same reason PodFinding is not overview's Finding: a certificate is not a
// pod, and folding this into PodFinding would give every pod-scoped rule a
// "which certificate" case it does not need.
type CertificateInsight struct {
	Severity Severity
	// Title is the problem, in a few words.
	Title string
	// Detail says what was observed, with the numbers in it.
	Detail string
	// Advice says what to do. An insight without one is an observation, and
	// there are enough of those in a certificate dump already.
	Advice string
}

// CertificateFindings assesses a certificate chain somebody asked to inspect.
//
// A PURE FUNCTION of the chain and a clock, exactly like AssessPod, so each
// rule is argued with in a test rather than only observed against a real
// cluster.
func CertificateFindings(chain CertificateChain, now time.Time) []CertificateInsight {
	insights := make([]CertificateInsight, 0, 4)

	insights = append(insights, expiryInsights(chain.Leaf, now)...)
	insights = append(insights, selfSignedInsight(chain.Leaf)...)
	insights = append(insights, keyMismatchInsight(chain)...)
	insights = append(insights, emptySANsInsight(chain.Leaf)...)

	sort.SliceStable(insights, func(i, j int) bool {
		return insights[i].Severity.rank() > insights[j].Severity.rank()
	})

	return insights
}

// expiryInsights covers the three ways a certificate's validity window can
// be worth raising: not yet valid, already expired, or closing in.
//
// ONE OF THESE, NEVER TWO. A certificate cannot be both expired and expiring
// soon, and reporting both would be the same fact said twice in different
// words.
func expiryInsights(cert Certificate, now time.Time) []CertificateInsight {
	switch {
	case now.Before(cert.NotBefore):
		return []CertificateInsight{{
			Severity: SeverityWarning,
			Title:    "Certificate is not yet valid",
			Detail:   fmt.Sprintf("Its validity begins on %s.", cert.NotBefore.Format(time.RFC3339)),
			Advice:   "Nothing will accept it until then. Check the issuing system's clock, and the certificate's requested start date.",
		}}
	case now.After(cert.NotAfter):
		return []CertificateInsight{{
			Severity: SeverityCritical,
			Title:    "Certificate has expired",
			Detail:   fmt.Sprintf("It expired on %s.", cert.NotAfter.Format(time.RFC3339)),
			Advice:   "Anything that checks validity is already refusing connections that present it. Rotate it.",
		}}
	case cert.ExpiresIn(now) <= certExpiryWarning:
		return []CertificateInsight{{
			Severity: SeverityWarning,
			Title:    "Certificate expires soon",
			Detail: fmt.Sprintf("It expires %s, on %s.",
				formatRemaining(cert.ExpiresIn(now)), cert.NotAfter.Format(time.RFC3339)),
			Advice: "Rotate it before it lapses.",
		}}
	default:
		return nil
	}
}

// formatRemaining renders a positive duration as "in N day(s)", coarsened to
// whole days — the unit an operator planning a rotation actually thinks in,
// not the hours and minutes a raw duration would print.
func formatRemaining(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days <= 0 {
		return "within a day"
	}
	if days == 1 {
		return "in 1 day"
	}
	return fmt.Sprintf("in %d days", days)
}

// selfSignedInsight says so, at the lowest severity: a self-signed leaf is
// routine for internal and development traffic and a fault for nothing on
// its own — it is worth knowing, not worth warning about.
func selfSignedInsight(leaf Certificate) []CertificateInsight {
	if !leaf.SelfSigned {
		return nil
	}
	return []CertificateInsight{{
		Severity: SeverityInfo,
		Title:    "Leaf certificate is self-signed",
		Detail:   "It is signed by its own key rather than by a certificate authority.",
		Advice:   "Expected for internal or development traffic. A client outside this trust boundary will refuse it unless it is told to trust this certificate directly.",
	}}
}

// keyMismatchInsight is the one CRITICAL insight that has nothing to do with
// the clock: a Secret whose key does not belong with its certificate fails
// every TLS handshake it is used for, the moment it is used.
func keyMismatchInsight(chain CertificateChain) []CertificateInsight {
	if chain.KeyMatches == nil || *chain.KeyMatches {
		return nil
	}
	return []CertificateInsight{{
		Severity: SeverityCritical,
		Title:    "Key does not match certificate",
		Detail:   "tls.key's public key does not correspond to the leaf certificate in tls.crt.",
		Advice:   "Every TLS handshake using this Secret will fail. It likely holds a key and a certificate from different pairs — replace one so they agree.",
	}}
}

// emptySANsInsight flags a certificate with no Subject Alternative Names.
//
// WORTH A WARNING, NOT JUST A NOTE: modern clients — Chrome since 2017, and
// Go's own crypto/tls — ignore the Subject's CN entirely and match only
// SANs, so a certificate with none of them fails hostname verification
// everywhere that matters, which reads as a broken TLS endpoint rather than
// as a hint to look at this Secret.
func emptySANsInsight(leaf Certificate) []CertificateInsight {
	if len(leaf.SANs) > 0 {
		return nil
	}
	return []CertificateInsight{{
		Severity: SeverityWarning,
		Title:    "No Subject Alternative Names",
		Detail:   "The certificate carries no SANs, only a Subject.",
		Advice:   "Add the hostnames (or IPs) this certificate should cover as SANs. Clients that ignore the Subject CN will refuse it as-is.",
	}}
}

// parsePEMCertificates decodes every "CERTIFICATE" PEM block in data, in the
// order they appear. A block of any other type is skipped rather than
// failing the call — see ParseCertificateChain's doc comment for why.
func parsePEMCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate

	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate: %w", err)
		}
		certs = append(certs, cert)
	}

	return certs, nil
}

// parsePrivateKey decodes the first PEM block of data as a private key,
// trying every encoding client-go and cert-manager actually produce: PKCS#1
// (openssl's "RSA PRIVATE KEY"), SEC 1 ("EC PRIVATE KEY"), and PKCS#8
// ("PRIVATE KEY", which covers RSA, EC and Ed25519 alike).
func parsePrivateKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("key of type %T does not sign", key)
		}
		return signer, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
}

// publicKeysEqual compares two public keys the way crypto/x509's own key
// types support: RSA, ECDSA and Ed25519 each implement
// `Equal(crypto.PublicKey) bool`, which is the standard library's own answer
// to "is this the same key" and avoids this package re-deriving it per
// algorithm.
func publicKeysEqual(a, b crypto.PublicKey) bool {
	equaler, ok := a.(interface{ Equal(x crypto.PublicKey) bool })
	if !ok {
		return false
	}
	return equaler.Equal(b)
}

// mapCertificate turns a parsed X.509 certificate into its display form.
func mapCertificate(cert *x509.Certificate) Certificate {
	return Certificate{
		Subject:            cert.Subject.String(),
		Issuer:             cert.Issuer.String(),
		SANs:               subjectAltNames(cert),
		NotBefore:          cert.NotBefore,
		NotAfter:           cert.NotAfter,
		SerialNumber:       cert.SerialNumber.String(),
		SignatureAlgorithm: cert.SignatureAlgorithm.String(),
		PublicKeyAlgorithm: cert.PublicKeyAlgorithm.String(),
		KeyBits:            keyBits(cert.PublicKey),
		IsCA:               cert.IsCA,
		SelfSigned:         isSelfSigned(cert),
	}
}

// subjectAltNames flattens the four kinds of SAN the standard library
// exposes separately into one ordered list, each printed the way it is
// conventionally written: DNS names and email addresses verbatim, IP
// addresses through net.IP's own String(), URIs through url.URL's own
// String().
func subjectAltNames(cert *x509.Certificate) []string {
	sans := make([]string, 0, len(cert.DNSNames)+len(cert.IPAddresses)+len(cert.EmailAddresses)+len(cert.URIs))
	sans = append(sans, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	sans = append(sans, cert.EmailAddresses...)
	for _, uri := range cert.URIs {
		sans = append(sans, uri.String())
	}
	return sans
}

// keyBits reports the size of pub in bits, or 0 for a key whose algorithm
// has no such notion, or that this build does not recognise.
func keyBits(pub any) int {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		return key.N.BitLen()
	case *ecdsa.PublicKey:
		return key.Curve.Params().BitSize
	default:
		return 0
	}
}

// isSelfSigned reports whether cert's own signature verifies against its own
// public key.
//
// THE SIGNATURE IS WHAT IS CHECKED, NOT JUST THE NAMES. Subject equal to
// Issuer is necessary but not sufficient — two different authorities can
// share a Subject by mistake or by policy — so this asks the one question
// that actually distinguishes "signed by itself" from "shares a name with
// its issuer": does the certificate's own public key produce its signature.
func isSelfSigned(cert *x509.Certificate) bool {
	if !bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return false
	}
	return cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}
