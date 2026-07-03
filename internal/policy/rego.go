package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/adefemi171/runeward/internal/profile"
	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
)

// RegoEngine is an authority engine backed by an OPA/Rego policy module. The
// module is compiled and prepared once at construction so a malformed policy
// fails fast at sandbox creation rather than silently mis-authorizing at run
// time. It is the third authority engine alongside the built-in glob [Engine]
// and the [CELEngine], for operators who already standardize on Rego/OPA.
//
// The engine evaluates a single query (default "data.runeward.decision")
// against the input document {tool, arg}. The query's result value may be
// EITHER a bare string verdict ("allow" | "deny" | "require-approval") OR a
// JSON object {"verdict": "...", "reason": "..."}; both shapes are supported.
// When the query is undefined, evaluation errors, or the verdict is
// unrecognized, the default verdict is returned with a nil Rule.
//
// RegoEngine implements [Evaluator]. It is safe for concurrent use:
// rego.PreparedEvalQuery.Eval is goroutine-safe.
type RegoEngine struct {
	pq  rego.PreparedEvalQuery
	def profile.Verdict
}

// defaultRegoQuery is the decision entrypoint used when none is configured.
const defaultRegoQuery = "data.runeward.decision"

// NewRego compiles and prepares module as a [RegoEngine]. query is the decision
// entrypoint; when empty it defaults to "data.runeward.decision". def is the
// verdict returned when the query is undefined or unrecognized; when empty it
// falls back to [profile.VerdictAllow]. Compilation/preparation errors are
// wrapped so misconfiguration fails fast at sandbox creation.
//
// OPA v1 uses Rego v1 syntax by default: modules should use the `if` and
// `contains` keywords and do not need `import future.keywords`.
func NewRego(module, query string, def profile.Verdict) (*RegoEngine, error) {
	if def == "" {
		def = profile.VerdictAllow
	}
	if query == "" {
		query = defaultRegoQuery
	}
	r := rego.New(
		rego.Query(query),
		rego.Module("policy.rego", module),
		// Pin to Rego v1 so modules use the `if`/`contains` keywords without
		// needing `import rego.v1`; the Go rego.New default is otherwise v0.
		rego.SetRegoVersion(ast.RegoV1),
	)
	pq, err := r.PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("policy: prepare rego query %q: %w", query, err)
	}
	return &RegoEngine{pq: pq, def: def}, nil
}

// Evaluate renders a [Decision] for a by evaluating the prepared query against
// the input {tool, arg}. On any failure (undefined result, eval error, or an
// unrecognized verdict) the engine's default verdict is returned with a nil
// Rule; it never panics.
func (e *RegoEngine) Evaluate(a Action) Decision {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := map[string]any{"tool": a.Tool, "arg": a.Arg}
	rs, err := e.pq.Eval(ctx, rego.EvalInput(input))
	if err != nil || len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{Verdict: e.def}
	}

	verdict, reason, ok := parseRegoResult(rs[0].Expressions[0].Value)
	if !ok {
		return Decision{Verdict: e.def}
	}
	return Decision{
		Verdict: verdict,
		Reason:  reason,
		Rule:    &profile.PolicyRule{Tool: a.Tool, Match: a.Arg, Verdict: verdict, Reason: reason},
	}
}

// parseRegoResult interprets a decision result value that may be either a bare
// string verdict or an object {"verdict": ..., "reason": ...}. It returns the
// recognized verdict, its reason, and whether a valid verdict was found.
func parseRegoResult(v any) (profile.Verdict, string, bool) {
	switch val := v.(type) {
	case string:
		return validVerdict(val, "")
	case map[string]any:
		verdict, _ := val["verdict"].(string)
		reason, _ := val["reason"].(string)
		return validVerdict(verdict, reason)
	default:
		return "", "", false
	}
}

// validVerdict reports whether s is a recognized verdict string, returning the
// typed verdict and its reason when it is.
func validVerdict(s, reason string) (profile.Verdict, string, bool) {
	switch profile.Verdict(s) {
	case profile.VerdictAllow, profile.VerdictDeny, profile.VerdictRequireApprove:
		return profile.Verdict(s), reason, true
	default:
		return "", "", false
	}
}
