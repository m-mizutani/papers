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

func TestSameIndex(t *testing.T) {
	spec := IndexSpec{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexDesc}}}
	want := spec.proto()

	withName := spec.proto()
	withName.Fields = append(withName.Fields, &adminpb.Index_IndexField{
		FieldPath: "__name__", ValueMode: &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_DESCENDING},
	})
	gt.True(t, sameIndex(want, withName))

	asc := IndexSpec{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexAsc}}}.proto()
	gt.False(t, sameIndex(want, asc))

	group := spec.proto()
	group.QueryScope = adminpb.Index_COLLECTION_GROUP
	gt.False(t, sameIndex(want, group))

	text := IndexSpec{CollectionGroup: colSearch, Fields: []IndexField{{"Text", IndexText}}}.proto()
	gt.True(t, sameIndex(text, IndexSpec{CollectionGroup: colSearch, Fields: []IndexField{{"Text", IndexText}}}.proto()))
	gt.False(t, sameIndex(text, IndexSpec{CollectionGroup: colSearch, Fields: []IndexField{{"Text", IndexAsc}}}.proto()))
}
