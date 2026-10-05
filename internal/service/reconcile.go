package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReconciliationCheck asserts a relation between two values produced by a step's
// source/target reconciliation queries (fully-qualified column aliases, e.g.
// "source.source_count", "target.target_count"). Op is the comparison to apply:
// "=" (equal, the default when empty) or "<=" (Left <= Right).
type ReconciliationCheck struct {
	Left  string
	Right string
	Op    string
}

// ReconciliationResult is the outcome of running a step's reconciliation
// queries: every value read from source/target, and which checks (if any)
// found a mismatch. It is stored as-is (JSON) in chat_migration_step.reconciliation.
type ReconciliationResult struct {
	Values map[string]any `json:"values"`
	Failed []string       `json:"failed_checks,omitempty"`
}

// Passed reports whether every check succeeded.
func (r *ReconciliationResult) Passed() bool { return len(r.Failed) == 0 }

// runReconciliation executes sourceSQL against oldDB and targetSQL against newDB,
// each expected to return exactly one row of named columns (aliased "source.x"/
// "target.x" by convention), then evaluates checks against the combined result.
// Either query may reference any of the named placeholders in params (e.g.
// :created_from, :session_id) -- each is substituted positionally, only in
// whichever of the two queries actually references it, per the project's
// reconciliation-script convention. Since source and target run against two
// separate databases, a value that lives only in one of them (e.g. a lookup
// table that exists only in new_db) must be resolved by the caller and passed
// in via params, not referenced directly in a cross-database subquery.
func runReconciliation(ctx context.Context, oldPool, newPool *pgxpool.Pool, sourceSQL, targetSQL string, params map[string]any, checks []ReconciliationCheck) (*ReconciliationResult, error) {
	sourceValues, err := queryReconciliationRow(ctx, oldPool, sourceSQL, params)
	if err != nil {
		return nil, fmt.Errorf("reconciliation source query: %w", err)
	}

	targetValues, err := queryReconciliationRow(ctx, newPool, targetSQL, params)
	if err != nil {
		return nil, fmt.Errorf("reconciliation target query: %w", err)
	}

	values := make(map[string]any, len(sourceValues)+len(targetValues))
	for k, v := range sourceValues {
		values[k] = v
	}

	for k, v := range targetValues {
		values[k] = v
	}

	return evaluateReconciliationChecks(values, checks)
}

// runTargetOnlyReconciliation is like runReconciliation, but for a step
// whose checks compare only values derived from new_db against each other
// (e.g. two counts over different new_db tables) -- there is nothing to read
// from old_db at all, so no source query (and no round trip to oldPool) is
// needed.
func runTargetOnlyReconciliation(ctx context.Context, newPool *pgxpool.Pool, targetSQL string, params map[string]any, checks []ReconciliationCheck) (*ReconciliationResult, error) {
	targetValues, err := queryReconciliationRow(ctx, newPool, targetSQL, params)
	if err != nil {
		return nil, fmt.Errorf("reconciliation target query: %w", err)
	}

	return evaluateReconciliationChecks(targetValues, checks)
}

// evaluateReconciliationChecks runs checks against the combined source/target
// values produced by a reconciliation's queries.
func evaluateReconciliationChecks(values map[string]any, checks []ReconciliationCheck) (*ReconciliationResult, error) {
	result := &ReconciliationResult{Values: values}
	for _, check := range checks {
		left, leftOK := values[check.Left]

		right, rightOK := values[check.Right]
		if !leftOK || !rightOK {
			result.Failed = append(result.Failed, fmt.Sprintf("%s or %s missing from reconciliation result", check.Left, check.Right))

			continue
		}

		op := check.Op
		if op == "" {
			op = "="
		}

		cmp, comparable := compareReconciliationValues(left, right)
		if !comparable {
			result.Failed = append(result.Failed, fmt.Sprintf("%s (%v) and %s (%v) are not comparable numeric values", check.Left, left, check.Right, right))

			continue
		}

		var passed bool

		switch op {
		case "=":
			passed = cmp == 0
		case "<=":
			passed = cmp <= 0
		default:
			return nil, fmt.Errorf("reconciliation check has unknown op %q", op)
		}

		if !passed {
			result.Failed = append(result.Failed, fmt.Sprintf("%s (%v) %s %s (%v) failed", check.Left, left, op, check.Right, right))
		}
	}

	return result, nil
}

// compareReconciliationValues compares two reconciliation values as integers,
// returning (cmp, true) with cmp negative/zero/positive as left is less than,
// equal to, or greater than right, or (0, false) if either value isn't an
// integer type (COUNT(*) results are always int64, but this guards against a
// malformed check referencing a non-numeric column).
func compareReconciliationValues(left, right any) (int, bool) {
	l, lok := toInt64(left)

	r, rok := toInt64(right)
	if !lok || !rok {
		return 0, false
	}

	switch {
	case l < r:
		return -1, true
	case l > r:
		return 1, true
	default:
		return 0, true
	}
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	}

	return 0, false
}

// queryReconciliationRow substitutes each ":name" placeholder in params that
// actually occurs in sql with a positional parameter, runs the query, and
// expects exactly one result row. A placeholder present in params but not
// referenced by this particular sql is simply not substituted.
func queryReconciliationRow(ctx context.Context, pool *pgxpool.Pool, sql string, params map[string]any) (map[string]any, error) {
	var args []any

	for name, value := range params {
		placeholder := ":" + name
		if !strings.Contains(sql, placeholder) {
			continue
		}

		args = append(args, value)
		sql = strings.ReplaceAll(sql, placeholder, fmt.Sprintf("$%d", len(args)))
	}

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return pgx.CollectExactlyOneRow(rows, pgx.RowToMap)
}
