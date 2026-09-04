package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// The keys a certificate-bearing Secret carries. tlsCertKey and tlsKeyKey
// are the two kubernetes.io/tls declares; caCertKey is not part of that
// type's required keys — it is a convention some issuers add alongside it
// (cert-manager does, when asked to include the issuing CA) — so it is read
// when present and simply absent otherwise.
const (
	tlsCertKey = "tls.crt"
	tlsKeyKey  = "tls.key"
	caCertKey  = "ca.crt"
)

// InspectTLSSecret parses one Secret's certificate material, on explicit
// request.
//
// THE SAME DISCIPLINE RevealSecretKey DOCUMENTS ABOVE. The Secret is fetched
// whole because the API offers no narrower read, but only tls.crt, tls.key
// and ca.crt are read from it here, at the boundary — everything else in the
// object, and the private key itself once it has been compared, travel no
// further than this function's local variables. The certificate is public
// material, but it lives inside the same object as the private key, and a
// read of that object is a read of that object regardless of which half
// somebody wants — so this is gated exactly like RevealSecretKey and never
// called except from a deliberate "Inspect certificate" press.
func (a *Adapter) InspectTLSSecret(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (domain.CertificateChain, error) {
	op := fmt.Sprintf("inspecting certificate of secret %q in %q", name, namespace)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.CertificateChain{}, err
	}

	secret, err := set.typed.CoreV1().Secrets(namespace.String()).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return domain.CertificateChain{}, classify(op, err)
	}

	crt, ok := secret.Data[tlsCertKey]
	if !ok {
		if secret.Type != corev1.SecretTypeTLS {
			// Not every Secret is a certificate, and guessing at which
			// arbitrary Opaque Secrets might hold one would be exactly the
			// kind of guess maskSecretData refuses to make for masking.
			return domain.CertificateChain{}, fmt.Errorf("%s: %w", op, domain.ErrNotTLSSecret)
		}
		// Declared as kubernetes.io/tls but missing the key that type
		// requires: the Secret is real, the key named by the caller is not
		// — the same distinction RevealSecretKey draws for a missing key.
		return domain.CertificateChain{}, fmt.Errorf("%s: %w", op, domain.ErrSecretKeyNotFound)
	}

	now := time.Now()

	chain, err := domain.ParseCertificateChain(crt, now)
	if err != nil {
		return domain.CertificateChain{}, fmt.Errorf("%s: %w", op, err)
	}

	if ca, ok := secret.Data[caCertKey]; ok {
		// Appended for chain display only. A ca.crt that fails to parse is
		// not this call's failure — the certificate being inspected is
		// tls.crt, and a malformed CA bundle beside it should not fail an
		// otherwise-valid inspection.
		if caChain, err := domain.ParseCertificateChain(ca, now); err == nil {
			chain.Intermediates = append(chain.Intermediates, caChain.Leaf)
			chain.Intermediates = append(chain.Intermediates, caChain.Intermediates...)
		}
	}

	if key, ok := secret.Data[tlsKeyKey]; ok {
		// An unparsable key is reported the same way as an absent one:
		// KeyMatches stays nil rather than claiming a mismatch this could
		// not actually establish.
		if matches, err := domain.VerifyKeyMatch(crt, key); err == nil {
			chain.KeyMatches = &matches
		}
	}

	return chain, nil
}
