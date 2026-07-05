package webhook

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Secret data keys for the persisted webhook serving certificate.
const (
	certSecretCertKey = "tls.crt"
	certSecretKeyKey  = "tls.key"
	certSecretCAKey   = "ca.crt"
)

// LoadOrCreateCert returns a serving certificate that is stable across restarts
// and shared by every webhook replica. It reads the named Secret; if the Secret
// already holds a complete cert it is reused, otherwise a fresh cert is
// generated and stored. On a create race (another replica won), the winner's
// Secret is read back, so all replicas converge on one CA — which is what makes
// running more than one replica behind the Service safe.
//
// When client is nil (no cluster access) it falls back to an ephemeral,
// in-memory cert, preserving the previous single-process behavior.
func LoadOrCreateCert(ctx context.Context, client kubernetes.Interface, namespace, secretName string, dnsNames []string) (certPEM, keyPEM, caPEM []byte, err error) {
	if client == nil || secretName == "" {
		return GenerateCert(dnsNames)
	}
	secrets := client.CoreV1().Secrets(namespace)

	if existing, gErr := secrets.Get(ctx, secretName, metav1.GetOptions{}); gErr == nil {
		if c, k, ca, ok := certFromSecret(existing); ok {
			return c, k, ca, nil
		}
	} else if !apierrors.IsNotFound(gErr) {
		return nil, nil, nil, fmt.Errorf("read cert secret %q: %w", secretName, gErr)
	}

	certPEM, keyPEM, caPEM, err = GenerateCert(dnsNames)
	if err != nil {
		return nil, nil, nil, err
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "runeward"},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			certSecretCertKey: certPEM,
			certSecretKeyKey:  keyPEM,
			certSecretCAKey:   caPEM,
		},
	}
	if _, cErr := secrets.Create(ctx, sec, metav1.CreateOptions{}); cErr != nil {
		if !apierrors.IsAlreadyExists(cErr) {
			return nil, nil, nil, fmt.Errorf("persist cert secret %q: %w", secretName, cErr)
		}
		// Lost the race: adopt the cert another replica just stored.
		existing, gErr := secrets.Get(ctx, secretName, metav1.GetOptions{})
		if gErr != nil {
			return nil, nil, nil, fmt.Errorf("read cert secret %q after race: %w", secretName, gErr)
		}
		if c, k, ca, ok := certFromSecret(existing); ok {
			return c, k, ca, nil
		}
		return nil, nil, nil, fmt.Errorf("cert secret %q exists but is incomplete", secretName)
	}
	return certPEM, keyPEM, caPEM, nil
}

// certFromSecret extracts the cert triple from a Secret, reporting ok=false if
// any part is missing.
func certFromSecret(s *corev1.Secret) (certPEM, keyPEM, caPEM []byte, ok bool) {
	c := s.Data[certSecretCertKey]
	k := s.Data[certSecretKeyKey]
	ca := s.Data[certSecretCAKey]
	if len(c) == 0 || len(k) == 0 || len(ca) == 0 {
		return nil, nil, nil, false
	}
	return c, k, ca, true
}
