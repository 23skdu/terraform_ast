package crossplane

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comp.yaml")
	content := `apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: example
spec:
  compositeTypeRef:
    apiVersion: example.org/v1alpha1
    kind: XExample
  resources:
    - name: bucket
      base:
        apiVersion: s3.aws.upbound.io/v1beta1
        kind: Bucket
      patches: []
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "Composition", c.Kind)
	assert.Equal(t, "bucket", c.Spec.Resources[0].Name)

	wrong := filepath.Join(dir, "wrong.yaml")
	require.NoError(t, os.WriteFile(wrong, []byte("kind: NotComposition\n"), 0o600))
	_, err = Load(wrong)
	assert.Error(t, err)

	_, err = Load(filepath.Join(dir, "missing.yaml"))
	assert.Error(t, err)

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("{{"), 0o600))
	_, err = Load(bad)
	assert.Error(t, err)
}

func makeComp(ct TypeRef, res ...ComposedResource) *Composition {
	return &Composition{Spec: CompositionSpec{CompositeTypeRef: ct, Resources: res}}
}

func baseRes(apiVersion, kind string) struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
} {
	return struct {
		APIVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
	}{APIVersion: apiVersion, Kind: kind}
}

func TestCompare(t *testing.T) {
	a := makeComp(TypeRef{APIVersion: "v1", Kind: "X"},
		ComposedResource{Name: "r1", Base: baseRes("v1", "Bucket")},
		ComposedResource{Name: "r2", Base: baseRes("v1", "Bucket")},
	)
	b := makeComp(TypeRef{APIVersion: "v2", Kind: "X"},
		ComposedResource{Name: "r1", Base: baseRes("v2", "Bucket")},
		ComposedResource{Name: "r3", Base: baseRes("v1", "Bucket")},
	)

	diff := Compare(a, b)
	assert.True(t, diff.HasDrift)
	assert.True(t, diff.CompositeTypeChanged)
	assert.Equal(t, []string{"r3"}, diff.Added)
	assert.Equal(t, []string{"r2"}, diff.Removed)
	assert.Equal(t, []string{"r1"}, diff.Changed)

	nilDiff := Compare(nil, b)
	assert.True(t, nilDiff.HasDrift)
	assert.Equal(t, "nil composition", nilDiff.Summary)

	inSync := Compare(a, a)
	assert.False(t, inSync.HasDrift)
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	c := &Composition{Kind: "Composition", APIVersion: "v1"}
	require.NoError(t, Save(path, c))
	_, err := os.Stat(path)
	assert.NoError(t, err)

	err = Save(filepath.Join(dir, "missing", "out.yaml"), c)
	assert.Error(t, err)

	err = Save(filepath.Join(dir, "bad.yaml"), nil)
	assert.Error(t, err)

	err = Save(filepath.Join(dir, "bad2.yaml"), &Composition{Metadata: map[string]any{"f": func() {}}})
	assert.Error(t, err)
}

func TestEqualComposed(t *testing.T) {
	a := ComposedResource{Name: "r", Base: baseRes("v1", "Bucket"), Patches: []map[string]any{{"a": 1}}}
	b := ComposedResource{Name: "r", Base: baseRes("v1", "Bucket"), Patches: []map[string]any{{"a": 1}}}
	assert.True(t, equalComposed(a, b))
	b.Base.Kind = "Other"
	assert.False(t, equalComposed(a, b))
}
