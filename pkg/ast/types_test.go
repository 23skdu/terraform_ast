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
		{"container mismatch", "list(object({a=string}))", "map(object({a=string}))", false, true},
		{"object attribute removed breaking", "object({a=string,b=number})", "object({a=string})", false, true},
		{"object new required attribute breaking", "object({a=string})", "object({a=string,b=number})", false, true},
		{"object new optional attribute compatible", "object({a=string})", "object({a=string,b=optional(number)})", true, false},
		{"object optional with default compatible", "object({a=string})", "object({a=string,b=optional(number,10)})", true, false},
		{"object attribute optional to required breaking", "object({a=string,b=optional(number)})", "object({a=string,b=number})", false, true},
		{"object inner attribute type changed breaking", "object({a=string})", "object({a=number})", false, true},
		{"nested list object compatible", "list(object({name=string}))", "list(object({name=string,tags=optional(map(string))}))", true, false},
		{"malformed object schema", "object(invalid", "object(invalid", true, false},
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

func TestParseObjectSchema_EdgeCases(t *testing.T) {
	_, ok := ParseObjectSchema("string")
	assert.False(t, ok)

	_, ok = ParseObjectSchema("object({")
	assert.False(t, ok)

	attrs, ok := ParseObjectSchema("object({ a = string, b = number, invalid_part })")
	assert.True(t, ok)
	assert.Contains(t, attrs, "a")
	assert.Contains(t, attrs, "b")
}
