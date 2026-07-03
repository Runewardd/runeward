// Package policy implements runeward's authority engine and cost/loop
// guardrails.
//
// The authority engine ([Engine]) evaluates an [Action] (a tool plus its
// primary argument) against an ordered list of [profile.PolicyRule] and renders
// a [Decision]. Rule evaluation is strictly first-match-wins: rules are tried in
// declaration order and the first rule whose tool and argument glob both match
// determines the verdict. There is deliberately no deny-precedence pass — the
// operator controls precedence purely through rule ordering.
//
// The cost/loop guardrails ([Guard]) live in guardrails.go and cap wall-clock
// time, exec count, and egress budget while detecting non-converging failure
// loops.
//
// The package depends only on the standard library.
package policy

import (
	"github.com/adefemi171/runeward/internal/profile"
)

// Action is a single tool invocation the engine is asked to authorize.
type Action struct {
	// Tool is the action surface, one of: "shell", "python", "node",
	// "file.read", "file.write", "file.edit", "net".
	Tool string
	// Arg is the primary argument the tool acts on: the command line for
	// exec tools, the path for file tools, or the hostname for "net".
	Arg string
}

// Evaluator renders an authority [Decision] for an [Action]. Both the built-in
// [Engine] and the [CELEngine] implement it, so the control plane can select an
// engine per profile without caring which is in use.
type Evaluator interface {
	Evaluate(Action) Decision
}

// Decision is the outcome of evaluating an [Action].
type Decision struct {
	// Verdict is the rendered authority decision.
	Verdict profile.Verdict
	// Reason is a human-readable explanation, sourced from the matched rule
	// when one exists.
	Reason string
	// Rule points at the rule that produced this decision, or nil when the
	// engine fell back to its default verdict.
	Rule *profile.PolicyRule
}

// Engine evaluates actions against an ordered rule set with a default verdict.
type Engine struct {
	rules []profile.PolicyRule
	def   profile.Verdict
}

// New builds an [Engine] from an ordered slice of rules and a default verdict
// used when no rule matches. If def is empty it falls back to
// [profile.VerdictAllow]. The rules slice is retained by reference; callers
// should not mutate it afterwards.
func New(rules []profile.PolicyRule, def profile.Verdict) *Engine {
	if def == "" {
		def = profile.VerdictAllow
	}
	return &Engine{rules: rules, def: def}
}

// Evaluate renders a [Decision] for a. Rules are tried in order and the first
// match wins. A rule matches when both:
//
//   - its Tool equals a.Tool, or the rule Tool is "*"; and
//   - a.Arg matches the rule's Match glob (an empty Match matches anything).
//
// When no rule matches, the engine's default verdict is returned with a nil
// Rule.
func (e *Engine) Evaluate(a Action) Decision {
	for i := range e.rules {
		r := &e.rules[i]
		if !toolMatches(r.Tool, a.Tool) {
			continue
		}
		if r.Match != "" && !matchGlob(r.Match, a.Arg) {
			continue
		}
		return Decision{Verdict: r.Verdict, Reason: r.Reason, Rule: r}
	}
	return Decision{Verdict: e.def, Reason: "", Rule: nil}
}

// toolMatches reports whether a rule tool selector matches an action tool. The
// selector "*" matches any tool.
func toolMatches(ruleTool, actionTool string) bool {
	return ruleTool == "*" || ruleTool == actionTool
}

// matchGlob reports whether s matches pattern as an anchored, full-string glob.
//
// Supported metacharacters:
//
//   - '*' matches any run of characters, INCLUDING '/' (unlike
//     [path/filepath.Match], which stops at a separator).
//   - '?' matches exactly one character (also including '/').
//   - all other characters match themselves literally.
//
// The whole of s must be consumed for a match. The implementation is a linear
// backtracking matcher (O(len(pattern)+len(s)) amortized) with no allocations.
func matchGlob(pattern, s string) bool {
	// si/pi walk s and pattern; star/ss remember the last '*' position and
	// where in s it started matching, so we can backtrack greedily.
	var (
		si, pi   int
		star     = -1
		starMark int
	)
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			si++
			pi++
		case pi < len(pattern) && pattern[pi] == '*':
			star = pi
			starMark = si
			pi++
		case star != -1:
			// Mismatch: let the last '*' absorb one more character of s.
			pi = star + 1
			starMark++
			si = starMark
		default:
			return false
		}
	}
	// Consume any trailing '*' in the pattern.
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
