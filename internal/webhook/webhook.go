// Package webhook implements the runeward admission webhook enforcing
// ClusterPolicy defaults and guardrails on Citadel and Cohort resources.
// The decision logic ([Decide]) is a pure function over a policy snapshot;
// [Server] wires it to the Kubernetes AdmissionReview contract.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// clusterPolicyGVR identifies the cluster-scoped ClusterPolicy resource.
var clusterPolicyGVR = schema.GroupVersionResource{
	Group:    "runeward.dev",
	Version:  "v1alpha1",
	Resource: "clusterpolicies",
}

// defaultedAnnotation marks resources whose profile came from a ClusterPolicy
// default.
const defaultedAnnotation = "runeward.dev/cluster-policy-defaulted"

// defaultPolicyCacheTTL bounds how stale a cached policy snapshot may be before
// the webhook stops trusting it during an API-server outage.
const defaultPolicyCacheTTL = 10 * time.Minute

// Server serves the admission webhook endpoints. It lists ClusterPolicies on
// every request so policy changes take effect without a restart.
type Server struct {
	dyn    dynamic.Interface
	logger *log.Logger

	// Last-known-good policy cache: when a live list fails, a recent cached
	// snapshot is used so a transient API-server blip doesn't deny every
	// admission. A never-populated or too-stale cache still fails closed.
	cacheMu     sync.RWMutex
	cached      []Policy
	cachedAt    time.Time
	cacheTTL    time.Duration
	cacheLoaded bool
}

// NewServer builds a Server backed by the given dynamic client. A nil logger
// uses the standard logger.
func NewServer(dyn dynamic.Interface, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{dyn: dyn, logger: logger, cacheTTL: policyCacheTTL()}
}

// policyCacheTTL reads RUNEWARD_WEBHOOK_POLICY_CACHE_TTL (a duration) or falls
// back to the default. "0"/"off" disables the cache (strict fail-closed).
func policyCacheTTL() time.Duration {
	v := os.Getenv("RUNEWARD_WEBHOOK_POLICY_CACHE_TTL")
	switch v {
	case "":
		return defaultPolicyCacheTTL
	case "0", "off", "none":
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	return defaultPolicyCacheTTL
}

// policySnapshot returns the current policy set. On a live-list success it
// refreshes the last-known-good cache. On failure it falls back to a cached
// snapshot within the TTL (fromCache=true); otherwise it returns the error so
// the caller can fail closed.
func (s *Server) policySnapshot(ctx context.Context) (policies []Policy, fromCache bool, err error) {
	live, listErr := s.listPolicies(ctx)
	if listErr == nil {
		s.cacheMu.Lock()
		s.cached = live
		s.cachedAt = time.Now()
		s.cacheLoaded = true
		s.cacheMu.Unlock()
		return live, false, nil
	}

	s.cacheMu.RLock()
	loaded, cached, at, ttl := s.cacheLoaded, s.cached, s.cachedAt, s.cacheTTL
	s.cacheMu.RUnlock()
	if loaded && ttl > 0 && time.Since(at) <= ttl {
		return cached, true, nil
	}
	return nil, false, listErr
}

// Handler returns the HTTP multiplexer exposing /validate, /mutate, and
// /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/validate", s.handleValidate)
	mux.HandleFunc("/mutate", s.handleMutate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	return mux
}

// listPolicies snapshots the current ClusterPolicies as []Policy.
func (s *Server) listPolicies(ctx context.Context) ([]Policy, error) {
	list, err := s.dyn.Resource(clusterPolicyGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list clusterpolicies: %w", err)
	}
	policies := make([]Policy, 0, len(list.Items))
	for i := range list.Items {
		obj := list.Items[i].Object
		allowed, _, _ := unstructured.NestedStringSlice(obj, "spec", "allowedProfiles")
		denied, _, _ := unstructured.NestedStringSlice(obj, "spec", "deniedProfiles")
		allowedNS, _, _ := unstructured.NestedStringSlice(obj, "spec", "allowedNamespaces")
		requiredLabels, _, _ := unstructured.NestedStringSlice(obj, "spec", "requiredLabels")
		defaultProfile, _, _ := unstructured.NestedString(obj, "spec", "defaultProfile")
		policies = append(policies, Policy{
			AllowedProfiles:   allowed,
			DeniedProfiles:    denied,
			AllowedNamespaces: allowedNS,
			RequiredLabels:    requiredLabels,
			DefaultProfile:    defaultProfile,
		})
	}
	return policies, nil
}

// handleValidate admits or rejects a Citadel/Cohort based on the policy set.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	review, req, obj, ok := s.decode(w, r)
	if !ok {
		return
	}

	policies, fromCache, err := s.policySnapshot(r.Context())
	if err != nil {
		// Fail closed: if we can't read the policy set (and have no fresh
		// cached snapshot) we can't prove the resource is allowed, so deny
		// rather than admit it ungoverned.
		s.logger.Printf("webhook: validate %s: %v (denying, fail-closed)", req.Resource.Resource, err)
		resp := &admissionv1.AdmissionResponse{
			UID:     req.UID,
			Allowed: false,
			Result:  &metav1.Status{Message: "runeward webhook could not evaluate ClusterPolicies: " + err.Error()},
		}
		writeReview(w, review, resp)
		return
	}
	if fromCache {
		s.logger.Printf("webhook: validate %s using last-known-good policy cache (live list failed)", req.Resource.Resource)
	}

	profileName, _, _ := unstructured.NestedString(obj.Object, "spec", "profile")
	allowed, _, reason := Decide(policies, req.Namespace, obj.GetLabels(), profileName)
	resp := &admissionv1.AdmissionResponse{UID: req.UID, Allowed: allowed}
	if !allowed {
		resp.Result = &metav1.Status{Message: reason}
		s.logger.Printf("webhook: deny %s %s/%s: %s", req.Resource.Resource, req.Namespace, req.Name, reason)
	}
	writeReview(w, review, resp)
}

// handleMutate defaults an empty spec.profile from a ClusterPolicy. It always
// admits; rejection is the validating webhook's job.
func (s *Server) handleMutate(w http.ResponseWriter, r *http.Request) {
	review, req, obj, ok := s.decode(w, r)
	if !ok {
		return
	}

	policies, _, err := s.policySnapshot(r.Context())
	if err != nil {
		s.logger.Printf("webhook: mutate %s: %v", req.Resource.Resource, err)
		writeReview(w, review, allowedResponse(req.UID))
		return
	}

	profileName, _, _ := unstructured.NestedString(obj.Object, "spec", "profile")
	_, mutated, _ := Decide(policies, req.Namespace, obj.GetLabels(), profileName)

	resp := &admissionv1.AdmissionResponse{UID: req.UID, Allowed: true}
	if mutated != "" && mutated != profileName {
		patch, err := profilePatch(obj, mutated)
		if err != nil {
			s.logger.Printf("webhook: build patch %s/%s: %v", req.Namespace, req.Name, err)
			writeReview(w, review, allowedResponse(req.UID))
			return
		}
		pt := admissionv1.PatchTypeJSONPatch
		resp.Patch = patch
		resp.PatchType = &pt
		s.logger.Printf("webhook: default %s %s/%s profile=%s", req.Resource.Resource, req.Namespace, req.Name, mutated)
	}
	writeReview(w, review, resp)
}

// jsonPatchOp is a single RFC 6902 JSON Patch operation.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// profilePatch builds the JSONPatch setting spec.profile and stamping the
// defaulted annotation, creating the annotations map when absent.
func profilePatch(obj *unstructured.Unstructured, profile string) ([]byte, error) {
	ops := []jsonPatchOp{{Op: "add", Path: "/spec/profile", Value: profile}}
	if obj.GetAnnotations() == nil {
		ops = append(ops, jsonPatchOp{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{defaultedAnnotation: "true"},
		})
	} else {
		ops = append(ops, jsonPatchOp{
			Op:    "add",
			Path:  "/metadata/annotations/" + escapeJSONPointer(defaultedAnnotation),
			Value: "true",
		})
	}
	return json.Marshal(ops)
}

// escapeJSONPointer escapes '~' and '/' per RFC 6901 for use in a patch path.
func escapeJSONPointer(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

// decode reads an AdmissionReview and unmarshals the incoming object. On
// failure it writes an error response and returns ok=false.
func (s *Server) decode(w http.ResponseWriter, r *http.Request) (review *admissionv1.AdmissionReview, req *admissionv1.AdmissionRequest, obj *unstructured.Unstructured, ok bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, nil, nil, false
	}
	review = &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, review); err != nil {
		http.Error(w, "decode AdmissionReview: "+err.Error(), http.StatusBadRequest)
		return nil, nil, nil, false
	}
	if review.Request == nil {
		http.Error(w, "AdmissionReview has no request", http.StatusBadRequest)
		return nil, nil, nil, false
	}
	req = review.Request
	obj = &unstructured.Unstructured{}
	if len(req.Object.Raw) > 0 {
		if err := obj.UnmarshalJSON(req.Object.Raw); err != nil {
			http.Error(w, "decode object: "+err.Error(), http.StatusBadRequest)
			return nil, nil, nil, false
		}
	}
	return review, req, obj, true
}

// allowedResponse is the fail-open response used when policies cannot be read.
func allowedResponse(uid types.UID) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{UID: uid, Allowed: true}
}

// writeReview wraps the response in an AdmissionReview envelope, preserving
// the request's TypeMeta.
func writeReview(w http.ResponseWriter, in *admissionv1.AdmissionReview, resp *admissionv1.AdmissionResponse) {
	out := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: resp,
	}
	if in != nil {
		out.TypeMeta = in.TypeMeta
		if out.APIVersion == "" {
			out.APIVersion = "admission.k8s.io/v1"
		}
		if out.Kind == "" {
			out.Kind = "AdmissionReview"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		http.Error(w, "encode response: "+err.Error(), http.StatusInternalServerError)
	}
}
