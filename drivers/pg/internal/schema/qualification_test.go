package schema

import (
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalManagedSQLUsesExplicitPublicNamespace(t *testing.T) {
	managedNames := `(?:graph|kind|node|edge|edge_identity|edge_property_constraint|edge_property_unique_value|dawgs_[a-z0-9_]+)`
	patterns := []string{
		`(?mi)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?` + managedNames + `\b`,
		`(?mi)^\s*alter\s+table\s+` + managedNames + `\b`,
		`(?mi)^\s*(?:insert\s+into|update|delete\s+from|truncate(?:\s+table)?)\s+` + managedNames + `\b`,
		`(?mi)^\s*drop\s+table\s+(?:if\s+exists\s+)?` + managedNames + `\b`,
		`(?mi)^\s*drop\s+trigger\b[^;\n]*\s+on\s+` + managedNames + `\b`,
		`(?mi)\breferences\s+` + managedNames + `\b`,
		`(?mi)\b(?:from|join)\s+` + managedNames + `\b`,
	}
	routinePatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?mi)^\s*create(?:\s+or\s+replace)?\s+(?:function|aggregate|type)\s+([a-z_][a-z0-9_.]*)`),
		regexp.MustCompile(`(?mi)^\s*drop\s+(?:function|aggregate|type)\s+(?:if\s+exists\s+)?([a-z_][a-z0-9_.]*)`),
	}
	for _, script := range []struct {
		name  string
		value string
	}{{"up", Up()}, {"down", Down()}} {
		for _, pattern := range patterns {
			if match := regexp.MustCompile(pattern).FindString(script.value); match != "" {
				t.Errorf("%s schema contains unqualified managed SQL %q", script.name, match)
			}
		}
		for _, pattern := range routinePatterns {
			for _, match := range pattern.FindAllStringSubmatch(script.value, -1) {
				if !strings.Contains(match[1], ".") {
					t.Errorf("%s schema contains unqualified routine or type %q", script.name, match[1])
				}
			}
		}
	}
}
