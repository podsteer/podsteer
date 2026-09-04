package k8s

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// --- test fixtures, generated here rather than read from disk. ---

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	return key
}

func mustPEMCert(t *testing.T, cn string, key *rsa.PrivateKey) []byte {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		DNSNames:     []string{cn},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func mustPEMKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// newTestAdapter returns an Adapter whose client for id is the fake
// clientset, bypassing the real kubeconfig-backed factory entirely — the
// same access a test in this package has to any other unexported field.
func newTestAdapter(id domain.ClusterID, objects ...runtime.Object) *Adapter {
	client := fake.NewSimpleClientset(objects...)
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{typed: client}
	return &Adapter{factory: factory}
}

// --- InspectTLSSecret --------------------------------------------------

func TestInspectTLSSecretReadsTheLeafAndMatchesTheKey(t *testing.T) {
	t.Parallel()

	key := mustRSAKey(t)
	certPEM := mustPEMCert(t, "app.example.com", key)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-tls", Namespace: "web"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": mustPEMKey(key),
		},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	chain, err := adapter.InspectTLSSecret(context.Background(), id, "web", "app-tls")
	if err != nil {
		t.Fatalf("InspectTLSSecret() error = %v", err)
	}
	if !strings.Contains(chain.Leaf.Subject, "app.example.com") {
		t.Errorf("Leaf.Subject = %q, want it to name app.example.com", chain.Leaf.Subject)
	}
	if chain.KeyMatches == nil || !*chain.KeyMatches {
		t.Errorf("KeyMatches = %v, want a pointer to true — tls.key is this certificate's own key", chain.KeyMatches)
	}
	if len(chain.Intermediates) != 0 {
		t.Errorf("Intermediates = %d, want 0 — no ca.crt was in this Secret", len(chain.Intermediates))
	}
}

func TestInspectTLSSecretDetectsAKeyThatDoesNotMatch(t *testing.T) {
	t.Parallel()

	certKey := mustRSAKey(t)
	otherKey := mustRSAKey(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-tls", Namespace: "web"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": mustPEMCert(t, "app.example.com", certKey),
			"tls.key": mustPEMKey(otherKey),
		},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	chain, err := adapter.InspectTLSSecret(context.Background(), id, "web", "app-tls")
	if err != nil {
		t.Fatalf("InspectTLSSecret() error = %v", err)
	}
	if chain.KeyMatches == nil || *chain.KeyMatches {
		t.Errorf("KeyMatches = %v, want a pointer to false", chain.KeyMatches)
	}
}

func TestInspectTLSSecretAddsCACertAsAnIntermediate(t *testing.T) {
	t.Parallel()

	leafKey := mustRSAKey(t)
	caKey := mustRSAKey(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-tls", Namespace: "web"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": mustPEMCert(t, "app.example.com", leafKey),
			"tls.key": mustPEMKey(leafKey),
			"ca.crt":  mustPEMCert(t, "internal-ca", caKey),
		},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	chain, err := adapter.InspectTLSSecret(context.Background(), id, "web", "app-tls")
	if err != nil {
		t.Fatalf("InspectTLSSecret() error = %v", err)
	}
	if len(chain.Intermediates) != 1 || !strings.Contains(chain.Intermediates[0].Subject, "internal-ca") {
		t.Errorf("Intermediates = %+v, want one entry naming internal-ca — ca.crt is shown for chain display", chain.Intermediates)
	}
}

func TestInspectTLSSecretWithoutAKeyLeavesKeyMatchesNil(t *testing.T) {
	t.Parallel()

	leafKey := mustRSAKey(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-tls", Namespace: "web"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": mustPEMCert(t, "app.example.com", leafKey),
		},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	chain, err := adapter.InspectTLSSecret(context.Background(), id, "web", "app-tls")
	if err != nil {
		t.Fatalf("InspectTLSSecret() error = %v", err)
	}
	if chain.KeyMatches != nil {
		t.Errorf("KeyMatches = %v, want nil — no tls.key was ever fetched to compare", chain.KeyMatches)
	}
}

func TestInspectTLSSecretRefusesASecretWithNoCertificate(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "web"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte("hunter2")},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	_, err := adapter.InspectTLSSecret(context.Background(), id, "web", "db-creds")
	if err == nil {
		t.Fatal("InspectTLSSecret() on an Opaque secret with no tls.crt, want an error")
	}
	if !errors.Is(err, domain.ErrNotTLSSecret) {
		t.Errorf("error = %v, want it to wrap domain.ErrNotTLSSecret", err)
	}
}

func TestInspectTLSSecretOnADeclaredTLSSecretMissingItsCertReportsTheMissingKey(t *testing.T) {
	t.Parallel()

	// Declared as kubernetes.io/tls but somehow carrying neither key — the
	// Secret is real, the key this call needs is not. The distinction
	// RevealSecretKey draws for the same situation.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-tls", Namespace: "web"},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{},
	}

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id, secret)

	_, err := adapter.InspectTLSSecret(context.Background(), id, "web", "empty-tls")
	if err == nil {
		t.Fatal("InspectTLSSecret() on a TLS secret with no tls.crt, want an error")
	}
	if !errors.Is(err, domain.ErrSecretKeyNotFound) {
		t.Errorf("error = %v, want it to wrap domain.ErrSecretKeyNotFound", err)
	}
}

func TestInspectTLSSecretOnAMissingSecretIsClassified(t *testing.T) {
	t.Parallel()

	id := domain.ClusterID("dev")
	adapter := newTestAdapter(id)

	_, err := adapter.InspectTLSSecret(context.Background(), id, "web", "does-not-exist")
	if err == nil {
		t.Fatal("InspectTLSSecret() on a Secret that does not exist, want an error")
	}
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("error = %v, want it to wrap ports.ErrNotFound", err)
	}
}
