// Package policybundle distributes runeward authority policies as signed,
// versioned OCI artifacts.
//
// A policy bundle is a single-layer OCI image manifest whose layer is the raw
// policy (a .rego module, or a TOML fragment carrying [[cel]] rules) and whose
// config blob records which engine the layer feeds and the optional Rego query.
// The bundle is versioned by the registry tag/digest it is pushed to, and it is
// signed with an ed25519 key so a consumer can pull it from an untrusted
// registry and refuse to load a policy that was tampered with in transit or at
// rest.
//
// # Signing scheme
//
// Signing the manifest digest directly is circular: the signature lives in the
// manifest annotations, so embedding it changes the very digest it signs.
// Instead we sign a small, canonical, versioned payload derived from the
// content-addressed digests of the config and layer blobs:
//
//	runeward.policy.bundle.v1
//	config=<config-blob-digest>
//	layer=<layer-blob-digest>
//
// Because both digests are sha256 sums of the blob bytes, this signature
// transitively covers the entire policy content (the .rego/.cel bytes) and the
// bundle metadata (engine + query, which live in the config blob). On pull the
// blob bytes are verified against the manifest's descriptor digests (oras'
// content.FetchAll fails closed on a mismatch), and the same canonical payload
// is recomputed from those descriptors and checked against the signature. An
// attacker who rewrites a blob must also rewrite the manifest digest to keep
// the fetch honest, which changes the signed payload and breaks the signature;
// they cannot forge a new signature without the private key. The signature,
// the base64 public key, and a short key id are stored as manifest annotations.
package policybundle

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

// Engine identifiers carried in the bundle config.
const (
	EngineRego = "rego"
	EngineCEL  = "cel"
)

// Media types used by runeward policy bundles.
const (
	// MediaTypeConfig is the bundle config blob (records engine + query).
	MediaTypeConfig = "application/vnd.runeward.policy.config.v1+json"
	// MediaTypeLayerRego is the layer media type for a Rego policy module.
	MediaTypeLayerRego = "application/vnd.runeward.policy.layer.v1.rego"
	// MediaTypeLayerCEL is the layer media type for a CEL TOML fragment.
	MediaTypeLayerCEL = "application/vnd.runeward.policy.layer.v1.cel+toml"
	// ArtifactType is set on the manifest to identify runeward policy bundles.
	ArtifactType = "application/vnd.runeward.policy.v1"
)

// Manifest annotation keys carrying the ed25519 signature material.
const (
	// AnnotationSignature holds the base64 ed25519 signature over the canonical
	// signing payload (see package doc).
	AnnotationSignature = "runeward.dev/signature"
	// AnnotationSigningKey holds the base64 ed25519 public key of the signer.
	AnnotationSigningKey = "runeward.dev/signing-key"
	// AnnotationKeyID holds a short fingerprint of the signing key.
	AnnotationKeyID = "runeward.dev/key-id"
)

// signingPayloadPrefix is the domain-separation tag of the v1 signing payload.
const signingPayloadPrefix = "runeward.policy.bundle.v1"

// Bundle is the decoded content of a policy bundle: an authority policy plus
// the metadata needed to load it into an engine.
type Bundle struct {
	// Engine is "rego" or "cel".
	Engine string
	// Query is the optional Rego decision entrypoint (ignored for CEL).
	Query string
	// Policy is the raw policy bytes: a .rego module for rego, or a TOML
	// fragment with [[cel]] rules for cel.
	Policy []byte
	// Annotations are the manifest annotations returned on pull (signature
	// material and OCI-standard keys). Ignored on push.
	Annotations map[string]string
}

// PushOptions tunes how a bundle is pushed to a remote registry.
type PushOptions struct {
	// PlainHTTP allows pushing to an http (non-TLS) registry, e.g. a local
	// test registry.
	PlainHTTP bool
}

// PullOptions tunes how a bundle is pulled from a remote registry.
type PullOptions struct {
	// PlainHTTP allows pulling from an http (non-TLS) registry.
	PlainHTTP bool
}

// bundleConfig is the JSON body of the config blob.
type bundleConfig struct {
	Engine string `json:"engine"`
	Query  string `json:"query,omitempty"`
}

// layerMediaType returns the layer media type for an engine.
func layerMediaType(engine string) (string, error) {
	switch engine {
	case EngineRego:
		return MediaTypeLayerRego, nil
	case EngineCEL:
		return MediaTypeLayerCEL, nil
	default:
		return "", fmt.Errorf("policybundle: unknown engine %q (want %q or %q)", engine, EngineRego, EngineCEL)
	}
}

// engineForLayer maps a layer media type back to its engine.
func engineForLayer(mediaType string) (string, error) {
	switch mediaType {
	case MediaTypeLayerRego:
		return EngineRego, nil
	case MediaTypeLayerCEL:
		return EngineCEL, nil
	default:
		return "", fmt.Errorf("policybundle: unrecognized policy layer media type %q", mediaType)
	}
}

// signingPayload builds the canonical bytes signed for a bundle from the
// content-addressed config and layer descriptors. See the package doc for the
// rationale behind signing these digests rather than the manifest digest.
func signingPayload(config, layer ocispec.Descriptor) []byte {
	var b strings.Builder
	b.WriteString(signingPayloadPrefix)
	b.WriteString("\nconfig=")
	b.WriteString(config.Digest.String())
	b.WriteString("\nlayer=")
	b.WriteString(layer.Digest.String())
	return []byte(b.String())
}

// keyID returns a short fingerprint of a public key: the first 8 bytes of
// SHA-256(pub), hex-encoded. This matches the ledger signer's convention.
func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// StripScheme removes an optional "oci://" prefix from a reference.
func StripScheme(ref string) string {
	return strings.TrimPrefix(ref, "oci://")
}

// Push builds a signed policy bundle and pushes it to the OCI reference ref
// (an optional "oci://" prefix is stripped). It returns the digest of the
// pushed manifest. The bundle is signed with priv; the signature and public
// key are attached as manifest annotations.
func Push(ctx context.Context, ref string, b *Bundle, priv ed25519.PrivateKey, opts PushOptions) (string, error) {
	repo, err := remote.NewRepository(StripScheme(ref))
	if err != nil {
		return "", fmt.Errorf("policybundle: parse reference %q: %w", ref, err)
	}
	repo.PlainHTTP = opts.PlainHTTP
	return pushTo(ctx, repo, repo.Reference.Reference, b, priv)
}

// pushTo packs, signs, and pushes a bundle to any oras target, tagging it with
// tag when tag is a non-empty, non-digest reference. It is the network-free
// core used by both [Push] and the tests.
func pushTo(ctx context.Context, target oras.Target, tag string, b *Bundle, priv ed25519.PrivateKey) (string, error) {
	if b == nil {
		return "", fmt.Errorf("policybundle: nil bundle")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("policybundle: signing key wrong size %d", len(priv))
	}
	layerType, err := layerMediaType(b.Engine)
	if err != nil {
		return "", err
	}

	cfgBytes, err := json.Marshal(bundleConfig{Engine: b.Engine, Query: b.Query})
	if err != nil {
		return "", fmt.Errorf("policybundle: marshal config: %w", err)
	}
	configDesc, err := oras.PushBytes(ctx, target, MediaTypeConfig, cfgBytes)
	if err != nil {
		return "", fmt.Errorf("policybundle: push config blob: %w", err)
	}
	layerDesc, err := oras.PushBytes(ctx, target, layerType, b.Policy)
	if err != nil {
		return "", fmt.Errorf("policybundle: push policy layer: %w", err)
	}

	pub := priv.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(priv, signingPayload(configDesc, layerDesc))
	annotations := map[string]string{
		AnnotationSignature:  base64.StdEncoding.EncodeToString(sig),
		AnnotationSigningKey: base64.StdEncoding.EncodeToString(pub),
		AnnotationKeyID:      keyID(pub),
	}

	manifestDesc, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, ArtifactType, oras.PackManifestOptions{
		ConfigDescriptor:    &configDesc,
		Layers:              []ocispec.Descriptor{layerDesc},
		ManifestAnnotations: annotations,
	})
	if err != nil {
		return "", fmt.Errorf("policybundle: pack manifest: %w", err)
	}

	if tag != "" && !strings.ContainsRune(tag, ':') {
		if err := target.Tag(ctx, manifestDesc, tag); err != nil {
			return "", fmt.Errorf("policybundle: tag %q: %w", tag, err)
		}
	}
	return manifestDesc.Digest.String(), nil
}

// Pull fetches and reconstructs a policy bundle from the OCI reference ref (an
// optional "oci://" prefix is stripped). When verify is non-nil the bundle's
// ed25519 signature is required and validated against verify; a missing or
// invalid signature is a fatal, fail-closed error. When verify is nil the
// bundle is returned without signature verification.
func Pull(ctx context.Context, ref string, verify ed25519.PublicKey, opts PullOptions) (*Bundle, error) {
	repo, err := remote.NewRepository(StripScheme(ref))
	if err != nil {
		return nil, fmt.Errorf("policybundle: parse reference %q: %w", ref, err)
	}
	repo.PlainHTTP = opts.PlainHTTP
	return pullFrom(ctx, repo, repo.Reference.Reference, verify)
}

// pullFrom is the network-free core used by both [Pull] and the tests. ref is
// the tag or digest to resolve within target.
func pullFrom(ctx context.Context, target oras.ReadOnlyTarget, ref string, verify ed25519.PublicKey) (*Bundle, error) {
	if ref == "" {
		ref = "latest"
	}
	_, manifestBytes, err := oras.FetchBytes(ctx, target, ref, oras.DefaultFetchBytesOptions)
	if err != nil {
		return nil, fmt.Errorf("policybundle: fetch manifest %q: %w", ref, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("policybundle: decode manifest: %w", err)
	}
	if len(manifest.Layers) != 1 {
		return nil, fmt.Errorf("policybundle: expected exactly 1 policy layer, got %d", len(manifest.Layers))
	}
	layerDesc := manifest.Layers[0]

	// Verify the signature (if required) before trusting any blob bytes.
	if verify != nil {
		if err := verifySignature(manifest, manifest.Config, layerDesc, verify); err != nil {
			return nil, err
		}
	}

	engine, err := engineForLayer(layerDesc.MediaType)
	if err != nil {
		return nil, err
	}

	// FetchAll verifies each blob's bytes against its descriptor digest, so a
	// tampered blob is rejected here even when no verify key is supplied.
	cfgBytes, err := content.FetchAll(ctx, target, manifest.Config)
	if err != nil {
		return nil, fmt.Errorf("policybundle: fetch config blob: %w", err)
	}
	var cfg bundleConfig
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, fmt.Errorf("policybundle: decode config: %w", err)
	}
	policyBytes, err := content.FetchAll(ctx, target, layerDesc)
	if err != nil {
		return nil, fmt.Errorf("policybundle: fetch policy layer: %w", err)
	}

	return &Bundle{
		Engine:      engine,
		Query:       cfg.Query,
		Policy:      policyBytes,
		Annotations: manifest.Annotations,
	}, nil
}

// verifySignature enforces that the manifest carries a valid ed25519 signature
// over the canonical signing payload, checked against pub. It fails closed when
// the signature annotation is missing or malformed.
func verifySignature(manifest ocispec.Manifest, config, layer ocispec.Descriptor, pub ed25519.PublicKey) error {
	sigB64 := manifest.Annotations[AnnotationSignature]
	if sigB64 == "" {
		return fmt.Errorf("policybundle: bundle is unsigned but a verify key was supplied")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("policybundle: decode signature: %w", err)
	}
	if !ed25519.Verify(pub, signingPayload(config, layer), sig) {
		return fmt.Errorf("policybundle: signature does not verify against the supplied key")
	}
	return nil
}

// DecodePublicKey parses a base64-encoded ed25519 public key.
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("policybundle: decode public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("policybundle: public key wrong size %d (want %d)", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// DecodePrivateKey parses a base64-encoded ed25519 private key.
func DecodePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("policybundle: decode private key: %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("policybundle: private key wrong size %d (want %d)", len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}

// EncodeKey base64-encodes raw key bytes.
func EncodeKey(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// KeyID returns the short fingerprint of a public key.
func KeyID(pub ed25519.PublicKey) string { return keyID(pub) }
