package ast

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeType(t *testing.T) {
	assert.Equal(t, "any", NormalizeType(""))
	assert.Equal(t, "any", NormalizeType("   "))
	assert.Equal(t, "string", NormalizeType("string"))
	assert.Equal(t, "list(string)", NormalizeType(" list( string ) "))
}

func TestCheckTypeCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		oldType    string
		newType    string
		compatible bool
		isBreaking bool
	}{
		{"identical string", "string", "string", true, false},
		{"identical list", "list(string)", "list(string)", true, false},
		{"any to concrete", "any", "string", false, true},
		{"empty to concrete", "", "string", false, true},
		{"concrete to any", "string", "any", true, false},
		{"list to set compatible", "list(string)", "set(string)", true, false},
		{"list to set mismatched inner", "list(string)", "set(number)", false, true},
		{"set to list breaking", "set(string)", "list(string)", false, true},
		{"string to number breaking", "string", "number", false, true},
		{"map to object breaking", "map(string)", "object({a=string})", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := CheckTypeCompatibility(tt.oldType, tt.newType)
			assert.Equal(t, tt.compatible, res.Compatible)
			assert.Equal(t, tt.isBreaking, res.IsBreaking)
			assert.NotEmpty(t, res.Reason)
		})
	}
}
