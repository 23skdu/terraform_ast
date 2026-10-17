package policy

import (
	"fmt"
	"strconv"
	"strings"
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
	Rules   []RegoRule
}

// ParseRego parses an OPA / Rego policy document.
func ParseRego(src string) (*RegoPolicy, error) {
	lines := strings.Split(src, "\n")
	policy := &RegoPolicy{
		Package: "terraform_ast.policy",
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

		if strings.HasPrefix(trimmed, "deny[") || strings.HasPrefix(trimmed, "violation[") {
			inRule = true
			ruleName := "deny"
			if strings.HasPrefix(trimmed, "violation[") {
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
	var violations []Violation

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
