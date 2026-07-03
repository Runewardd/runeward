package policy

import (
	"fmt"

	"github.com/adefemi171/runeward/internal/profile"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// CELEngine is an authority engine backed by Common Expression Language (CEL)
// rules. Each rule is a boolean CEL expression over the variables `tool` and
// `arg`; rules are evaluated in declaration order and the first whose
// expression is true renders its verdict. If no rule matches, the default
// verdict applies. It is the alternative to the built-in glob [Engine] for
// operators who already standardize on CEL (or want richer predicates than
// tool+glob).
//
// CELEngine implements [Evaluator]. It is safe for concurrent use: compiled
// programs are immutable and cel.Program.Eval is goroutine-safe.
type CELEngine struct {
	rules []compiledRule
	def   profile.Verdict
}

type compiledRule struct {
	prg     cel.Program
	verdict profile.Verdict
	reason  string
	src     profile.CELRule
}

// NewCEL compiles rules into a [CELEngine]. Each rule's Expr must be a valid CEL
// expression yielding a bool over the variables `tool` (string) and `arg`
// (string). Compilation errors (syntax, type, or a non-boolean result) are
// reported with the offending expression so misconfiguration fails fast at
// sandbox creation rather than silently mis-authorizing at run time. If def is
// empty it falls back to [profile.VerdictAllow].
func NewCEL(rules []profile.CELRule, def profile.Verdict) (*CELEngine, error) {
	if def == "" {
		def = profile.VerdictAllow
	}
	env, err := cel.NewEnv(
		cel.Variable("tool", cel.StringType),
		cel.Variable("arg", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("policy: build CEL env: %w", err)
	}

	compiled := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		if r.Expr == "" {
			return nil, fmt.Errorf("policy: cel rule %d: empty expr", i)
		}
		ast, iss := env.Compile(r.Expr)
		if iss != nil && iss.Err() != nil {
			return nil, fmt.Errorf("policy: cel rule %d (%q): %w", i, r.Expr, iss.Err())
		}
		if ast.OutputType() != cel.BoolType {
			return nil, fmt.Errorf("policy: cel rule %d (%q): expression must return bool, got %s", i, r.Expr, ast.OutputType())
		}
		prg, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("policy: cel rule %d (%q): program: %w", i, r.Expr, err)
		}
		verdict := r.Verdict
		if verdict == "" {
			verdict = profile.VerdictAllow
		}
		compiled = append(compiled, compiledRule{prg: prg, verdict: verdict, reason: r.Reason, src: r})
	}
	return &CELEngine{rules: compiled, def: def}, nil
}

// Evaluate renders a [Decision] for a. The first rule whose expression
// evaluates to true wins; a rule whose evaluation errors is skipped (treated as
// non-matching) so a single bad rule cannot wedge the whole engine. When no
// rule matches, the default verdict is returned with a nil Rule.
func (e *CELEngine) Evaluate(a Action) Decision {
	vars := map[string]any{"tool": a.Tool, "arg": a.Arg}
	for i := range e.rules {
		r := &e.rules[i]
		out, _, err := r.prg.Eval(vars)
		if err != nil {
			continue
		}
		if isTrue(out) {
			rule := r.src // stable copy for the pointer in Decision
			return Decision{
				Verdict: r.verdict,
				Reason:  r.reason,
				Rule:    &profile.PolicyRule{Tool: a.Tool, Match: rule.Expr, Verdict: r.verdict, Reason: r.reason},
			}
		}
	}
	return Decision{Verdict: e.def}
}

// isTrue reports whether a CEL result ref.Val is the boolean true.
func isTrue(v ref.Val) bool {
	b, ok := v.(types.Bool)
	return ok && bool(b)
}
