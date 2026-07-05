package backend

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureNamespaceSetsPSALabels(t *testing.T) {
	k := &K8s{client: fake.NewSimpleClientset(), namespace: "runeward"}
	if err := k.ensureNamespace(context.Background()); err != nil {
		t.Fatalf("ensureNamespace: %v", err)
	}
	ns, err := k.client.CoreV1().Namespaces().Get(context.Background(), "runeward", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	want := map[string]string{
		"pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/warn":    "baseline",
		"pod-security.kubernetes.io/audit":   "baseline",
	}
	for key, val := range want {
		if got := ns.Labels[key]; got != val {
			t.Errorf("namespace label %q = %q, want %q", key, got, val)
		}
	}
	if ns.Labels[labelKey(labelManaged)] != "true" {
		t.Errorf("managed label missing: %v", ns.Labels)
	}
}

func TestEnsureNamespacePatchesExistingPSALabels(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "runeward",
			Labels: map[string]string{labelKey(labelManaged): "true"},
		},
	})
	k := &K8s{client: client, namespace: "runeward"}
	if err := k.ensureNamespace(context.Background()); err != nil {
		t.Fatalf("ensureNamespace: %v", err)
	}
	ns, err := k.client.CoreV1().Namespaces().Get(context.Background(), "runeward", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "privileged" {
		t.Errorf("existing namespace not patched with PSA labels: %v", ns.Labels)
	}
}

func TestEnsureNetworkPolicyGatedByEnv(t *testing.T) {
	k := &K8s{client: fake.NewSimpleClientset(), namespace: "runeward"}
	// Default (env unset) => no policy created.
	if err := k.ensureNetworkPolicy(context.Background()); err != nil {
		t.Fatalf("ensureNetworkPolicy (disabled): %v", err)
	}
	list, err := k.client.NetworkingV1().NetworkPolicies("runeward").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list network policies: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected no network policy when disabled, got %d", len(list.Items))
	}

	t.Setenv("RUNEWARD_K8S_NETWORK_POLICY", "true")
	if err := k.ensureNetworkPolicy(context.Background()); err != nil {
		t.Fatalf("ensureNetworkPolicy (enabled): %v", err)
	}
	np, err := k.client.NetworkingV1().NetworkPolicies("runeward").Get(context.Background(), "runeward-default-deny", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get default-deny policy: %v", err)
	}
	if len(np.Spec.Ingress) != 0 {
		t.Errorf("expected deny-all ingress (no rules), got %d", len(np.Spec.Ingress))
	}
	if len(np.Spec.Egress) != 1 || len(np.Spec.Egress[0].Ports) != 2 {
		t.Fatalf("expected a single DNS egress rule with 2 ports, got %+v", np.Spec.Egress)
	}
	for _, p := range np.Spec.Egress[0].Ports {
		if p.Port == nil || p.Port.IntValue() != 53 {
			t.Errorf("expected DNS port 53, got %+v", p.Port)
		}
	}

	// Idempotent: a second call must not error even though it already exists.
	if err := k.ensureNetworkPolicy(context.Background()); err != nil {
		t.Fatalf("ensureNetworkPolicy (idempotent): %v", err)
	}
}
