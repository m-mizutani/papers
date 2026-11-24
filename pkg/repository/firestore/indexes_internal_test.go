package firestore

import (
	"testing"

	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"github.com/m-mizutani/gt"
)

func TestRequiredIndexesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range RequiredIndexes {
		gt.NotEqual(t, spec.CollectionGroup, "")
		gt.A(t, spec.Fields).Longer(0)
		gt.False(t, seen[spec.String()]).Describef("duplicate index %s", spec)
		seen[spec.String()] = true
		for _, f := range spec.proto().GetFields() {
			gt.True(t, f.GetValueMode() != nil).Describef("index %s field %s has no mode", spec, f.GetFieldPath())
		}
	}
}

func TestIndexProtoDensity(t *testing.T) {
	// T28: ordered indexes ask for DENSE; search indexes leave it unset.
	for _, spec := range RequiredIndexes {
		want := adminpb.Index_DENSE
		if spec.isSearch() {
			want = adminpb.Index_DENSITY_UNSPECIFIED
		}
		gt.Equal(t, spec.proto().GetDensity(), want)
		gt.Equal(t, spec.proto().GetApiScope(), adminpb.Index_ANY_API)
		gt.Equal(t, spec.proto().GetQueryScope(), spec.scope())
		gt.Equal(t, spec.Group, spec.CollectionGroup == colProperties)
	}
}
