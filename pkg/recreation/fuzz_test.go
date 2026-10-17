package recreation

import (
	"strings"
	"testing"
)

// FuzzParseProviderSchema ensures the schema parser never panics on arbitrary JSON input.
func FuzzParseProviderSchema(f *testing.F) {
	for _, seed := range []string{
		sampleProviderSchema,
		"{}",
		`{"format_version": "1.0"}`,
		`{"provider_schemas": null}`,
		`{"provider_schemas": {"p": {"resource_schemas": {"r": {"block": {"attributes": {"a": {"requires_replace": true}}}}}}}}`,
		"invalid json",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		ps, err := ParseProviderSchema(strings.NewReader(data))
		if err != nil {
			return
		}
		if ps != nil {
			_ = ps.ExtractForceNewDescriptors()
		}
	})
}
