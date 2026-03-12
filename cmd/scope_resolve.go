package cmd

import (
	"context"
	"fmt"
	"strings"
)

// ResolveScope resolves a user-provided --scope value to the best-matching
// scope in the database. Unlike QualifyScope (which blindly prepends the
// project prefix), ResolveScope checks what scopes actually exist and falls
// back progressively:
//
//  1. Empty → empty (caller handles DefaultScope).
//  2. Leading "/" → literal scope (strip prefix, same as QualifyScope).
//  3. Qualified form (prefix.input) exists → use it.
//  4. Bare input exists as a top-level scope → use it.
//  5. Suffix match: exactly one scope ends with "."+input → use it.
//     Multiple matches → warn and fall back to qualified.
//  6. Fall back to qualified form (conservative default).
//
// This method is for read-path commands only (recall, search, list, show,
// stats, conflicts, export). Write commands use QualifyScope for deterministic
// scope placement.
func (a *App) ResolveScope(ctx context.Context, input string) string {
	if input == "" {
		return ""
	}

	// Literal/cross-project escape hatch.
	if strings.HasPrefix(input, "/") {
		return input[1:]
	}

	// No prefix configured — no ambiguity possible.
	if a.Config.ScopePrefix == "" {
		return input
	}

	qualified := a.Config.QualifyScope(input)

	// Step 3: qualified form exists?
	if _, err := a.Scopes.Get(ctx, qualified); err == nil {
		return qualified
	}

	// Step 4: bare input exists as a scope?
	if _, err := a.Scopes.Get(ctx, input); err == nil {
		return input
	}

	// Step 5: suffix match across all scopes.
	scopes, err := a.Scopes.List(ctx)
	if err != nil {
		return qualified // DB error — fall back conservatively
	}

	suffix := "." + input
	var matches []string
	for _, s := range scopes {
		if strings.HasSuffix(s.Path, suffix) {
			matches = append(matches, s.Path)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0]
	case 0:
		return qualified
	default:
		fmt.Fprintf(a.Stderr, "warning: ambiguous scope %q matches: %s (using %s)\n",
			input, strings.Join(matches, ", "), qualified)
		return qualified
	}
}
