package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/open-policy-agent/opa/v1/rego"
)

// RegoRule represents a single parsed rule from a Rego policy file.
type RegoRule struct {
	Name       string
	Conditions []RegoCondition
	Message    string
}

// RegoCondition represents a condition predicate within a Rego rule.
type RegoCondition struct {
	Field    string
	Operator string
	Value    string
}

// RegoPolicy contains parsed Rego policy rules.
type RegoPolicy struct {
	Package string
	Source  string
	Rules   []RegoRule
}

// LoadRego reads and parses an OPA / Rego policy document from a file.
func LoadRego(path string) (*RegoPolicy, error) {
	// #nosec G304 -- operator specified policy file path
	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read rego policy file %s: %w", path, err)
	}
	return ParseRego(string(src))
}

// ParseRego parses an OPA / Rego policy document.
func ParseRego(src string) (*RegoPolicy, error) {
	lines := strings.Split(src, "\n")
	policy := &RegoPolicy{
		Package: "terraform_ast.policy",
		Source:  src,
	}

	var currentRule *RegoRule
	inRule := false

	for lineNum, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "package ") {
			policy.Package = strings.TrimSpace(strings.TrimPrefix(trimmed, "package "))
			continue
		}

		if strings.HasPrefix(trimmed, "deny[") || strings.HasPrefix(trimmed, "violation[") ||
			strings.HasPrefix(trimmed, "deny contains") || strings.HasPrefix(trimmed, "violation contains") ||
			strings.HasPrefix(trimmed, "deny if") || strings.HasPrefix(trimmed, "violation if") ||
			strings.HasPrefix(trimmed, "deny =") || strings.HasPrefix(trimmed, "violation =") ||
			strings.HasPrefix(trimmed, "deny :=") || strings.HasPrefix(trimmed, "violation :=") {
			inRule = true
			ruleName := "deny"
			if strings.HasPrefix(trimmed, "violation") {
				ruleName = "violation"
			}
			currentRule = &RegoRule{
				Name: ruleName,
			}
			continue
		}

		if inRule {
			if trimmed == "}" {
				if currentRule != nil {
					policy.Rules = append(policy.Rules, *currentRule)
				}
				currentRule = nil
				inRule = false
				continue
			}

			if strings.HasPrefix(trimmed, "msg :=") || strings.HasPrefix(trimmed, "msg =") {
				parts := strings.SplitN(trimmed, "=", 2)
				if len(parts) == 2 {
					currentRule.Message = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				}
				continue
			}

			if strings.Contains(trimmed, ":=") {
				continue
			}

			cond, err := parseCondition(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNum+1, err)
			}
			currentRule.Conditions = append(currentRule.Conditions, cond)
		}
	}

	if inRule && currentRule != nil {
		policy.Rules = append(policy.Rules, *currentRule)
	}

	if len(policy.Rules) == 0 {
		return nil, fmt.Errorf("no valid rules found in Rego policy")
	}

	return policy, nil
}

func parseCondition(condStr string) (RegoCondition, error) {
	condStr = strings.TrimSuffix(condStr, ";")
	ops := []string{">=", "<=", "==", "!=", ">", "<"}
	for _, op := range ops {
		if strings.Contains(condStr, op) {
			parts := strings.SplitN(condStr, op, 2)
			field := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			field = strings.TrimPrefix(field, "input.")

			validFields := map[string]struct{}{
				"breaking_changes": {},
				"warnings":         {},
				"is_downgrade":     {},
				"bump_type":        {},
			}
			if _, ok := validFields[field]; !ok {
				return RegoCondition{}, fmt.Errorf("unrecognized policy field: %s", field)
			}

			return RegoCondition{
				Field:    field,
				Operator: op,
				Value:    val,
			}, nil
		}
	}
	return RegoCondition{}, fmt.Errorf("unsupported condition expression: %s", condStr)
}

// EvaluateRego evaluates an Input against a parsed RegoPolicy and returns violations.
func EvaluateRego(policy *RegoPolicy, input Input) []Violation {
	if policy == nil {
		return nil
	}
	inputMap := map[string]any{
		"breaking_changes": input.BreakingChanges,
		"warnings":         input.Warnings,
		"is_downgrade":     input.IsDowngrade,
		"bump_type":        input.BumpType,
	}

	violations, err := EvaluateRegoArbitrary(context.Background(), policy, inputMap)
	if err == nil && len(violations) > 0 {
		return violations
	}

	for _, rule := range policy.Rules {
		matched := true
		for _, cond := range rule.Conditions {
			if !matchesCondition(cond, input) {
				matched = false
				break
			}
		}
		if matched {
			msg := rule.Message
			if msg == "" {
				msg = fmt.Sprintf("Rego policy rule '%s' violated", rule.Name)
			}
			violations = append(violations, Violation{
				Rule:    rule.Name,
				Message: msg,
			})
		}
	}
	return violations
}

// EvaluateRegoArbitrary evaluates a Rego policy using the official Open Policy Agent runtime.
func EvaluateRegoArbitrary(ctx context.Context, policy *RegoPolicy, input any) ([]Violation, error) {
	if policy == nil || policy.Source == "" {
		return nil, nil
	}
	src := policy.Source
	if !strings.Contains(src, "import rego.v1") && !strings.Contains(src, "import future.keywords") {
		if strings.Contains(src, "contains") || strings.Contains(src, " if ") || strings.Contains(src, " if{") {
			pkgHeader := "package " + policy.Package
			src = strings.Replace(src, pkgHeader, pkgHeader+"\nimport rego.v1\n", 1)
		}
	}
	queryStr := fmt.Sprintf("data.%s", policy.Package)
	r := rego.New(
		rego.Query(queryStr),
		rego.Module("policy.rego", src),
		rego.Input(input),
	)
	rs, err := r.Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("opa evaluation error: %w", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return nil, nil
	}

	var violations []Violation
	exprVal := rs[0].Expressions[0].Value
	if valMap, ok := exprVal.(map[string]any); ok {
		for _, ruleKey := range []string{"deny", "violation"} {
			if raw, ok := valMap[ruleKey]; ok {
				switch v := raw.(type) {
				case []any:
					for _, item := range v {
						violations = append(violations, Violation{
							Rule:    ruleKey,
							Message: fmt.Sprintf("%v", item),
						})
					}
				case map[string]any:
					for _, item := range v {
						violations = append(violations, Violation{
							Rule:    ruleKey,
							Message: fmt.Sprintf("%v", item),
						})
					}
				case string:
					violations = append(violations, Violation{
						Rule:    ruleKey,
						Message: v,
					})
				case bool:
					if v {
						violations = append(violations, Violation{
							Rule:    ruleKey,
							Message: fmt.Sprintf("Rego policy rule '%s' violated", ruleKey),
						})
					}
				}
			}
		}
	}
	return violations, nil
}

func matchesCondition(cond RegoCondition, input Input) bool {
	switch cond.Field {
	case "breaking_changes":
		targetVal, err := strconv.Atoi(cond.Value)
		if err != nil {
			return false
		}
		return compareInt(input.BreakingChanges, cond.Operator, targetVal)

	case "warnings":
		targetVal, err := strconv.Atoi(cond.Value)
		if err != nil {
			return false
		}
		return compareInt(input.Warnings, cond.Operator, targetVal)

	case "is_downgrade":
		expected := cond.Value == "true"
		actual := input.IsDowngrade
		if cond.Operator == "==" {
			return actual == expected
		}
		if cond.Operator == "!=" {
			return actual != expected
		}
		return false

	case "bump_type":
		actual := strings.ToUpper(strings.TrimSpace(input.BumpType))
		expected := strings.ToUpper(strings.TrimSpace(cond.Value))
		if cond.Operator == "==" {
			return actual == expected
		}
		if cond.Operator == "!=" {
			return actual != expected
		}
		return false

	default:
		return false
	}
}

func compareInt(actual int, op string, target int) bool {
	switch op {
	case ">":
		return actual > target
	case ">=":
		return actual >= target
	case "<":
		return actual < target
	case "<=":
		return actual <= target
	case "==":
		return actual == target
	case "!=":
		return actual != target
	default:
		return false
	}
}
