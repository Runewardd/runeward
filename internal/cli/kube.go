package cli

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeConfig builds a Kubernetes REST config, preferring the in-cluster service
// account (so the controller works when deployed as a Pod) and falling back to
// the ambient kubeconfig. $RUNEWARD_KUBE_CONTEXT pins the context, matching the
// convention used by the Kubernetes backend.
func kubeConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if kctx := os.Getenv("RUNEWARD_KUBE_CONTEXT"); kctx != "" {
		overrides.CurrentContext = kctx
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}

// kubeNamespace returns the namespace the controller/installer operates in.
func kubeNamespace() string {
	if ns := os.Getenv("RUNEWARD_K8S_NAMESPACE"); ns != "" {
		return ns
	}
	return "runeward"
}
