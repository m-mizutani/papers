package firestore

import (
	"strings"

	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
)

// Firestore Enterprise creates no index automatically and runs a query
// without a matching index as a collection scan, which is slow and billed
// per scanned document. Every query of this package is therefore listed
// here, and `papers firestore migrate` creates what is missing. Adding a
// query means adding its index to RequiredIndexes.

// IndexMode is how one field of an index is indexed.
type IndexMode string

const (
	IndexAsc      IndexMode = "asc"
	IndexDesc     IndexMode = "desc"
	IndexContains IndexMode = "array-contains"
	IndexText     IndexMode = "text"
)

type IndexField struct {
	Path string
	Mode IndexMode
}

// IndexSpec is one index papers needs, scoped to a collection group.
type IndexSpec struct {
	CollectionGroup string
	Fields          []IndexField
	// Query names the query the index serves, for humans.
	Query string
}

func (s IndexSpec) String() string {
	parts := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		parts[i] = f.Path + " " + string(f.Mode)
	}
	return s.CollectionGroup + " (" + strings.Join(parts, ", ") + ")"
}

// RequiredIndexes are the indexes the repository's queries need. Queries
// that read a whole small collection (the blocks of a paper)
// or that range over document IDs need no index.
var RequiredIndexes = []IndexSpec{
	{CollectionGroup: colTokens, Fields: []IndexField{{"UserID", IndexAsc}}, Query: "ListTokens"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"ParentID", IndexAsc}, {"Title", IndexAsc}}, Query: "ListChildren, ListChildrenOf"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"Visibility", IndexAsc}, {"CreatedBy.UserID", IndexAsc}, {"Title", IndexAsc}}, Query: "ListPrivatePapers"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"CreatedBy.UserID", IndexAsc}, {"DeletedAt", IndexAsc}}, Query: "ListTrash"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"DeletedAt", IndexAsc}}, Query: "ListTrashedBefore"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexAsc}}, Query: "ListBlocksRange, Tx.FirstAfter, Tx.Children"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexDesc}}, Query: "Tx.Last"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"Type", IndexAsc}, {"Order", IndexAsc}}, Query: "ListPaperBlocks"},
	{CollectionGroup: colVersions, Fields: []IndexField{{"Number", IndexDesc}}, Query: "ListVersions"},
	{CollectionGroup: colEvents, Fields: []IndexField{{"Event.At", IndexAsc}}, Query: "ListEvents, Watch"},
	{CollectionGroup: colAttachments, Fields: []IndexField{{"SHA256", IndexAsc}}, Query: "FindAttachmentByHash"},
	{CollectionGroup: colJobs, Fields: []IndexField{{"State", IndexAsc}, {"LeaseUntil", IndexAsc}}, Query: "ListRunnableJobs"},
	{CollectionGroup: colJobItems, Fields: []IndexField{{"Expanded", IndexAsc}, {"Depth", IndexAsc}}, Query: "ListUnexpandedJobItems"},
	{CollectionGroup: colJobItems, Fields: []IndexField{{"Stage", IndexAsc}, {"Depth", IndexAsc}}, Query: "ListJobItemsAt"},
	{CollectionGroup: colSearch, Fields: []IndexField{{"Text", IndexText}}, Query: "Search"},
}

// TTLFields are the fields whose time value expires the document.
var TTLFields = []struct{ CollectionGroup, Field string }{
	{colSessions, "ExpiresAt"},
	{colEvents, "ExpireAt"},
	{colJobs, "ExpireAt"},
	{colJobItems, "ExpireAt"},
}

// isSearch reports whether s is a full-text search index.
func (s IndexSpec) isSearch() bool {
	for _, f := range s.Fields {
		if f.Mode == IndexText {
			return true
		}
	}
	return false
}

// proto is the index to create for s. Ordered indexes ask for the DENSE
// density explicitly (the Enterprise default) so that what the server
// returns compares deterministically; search indexes leave it to the server.
func (s IndexSpec) proto() *adminpb.Index {
	idx := &adminpb.Index{QueryScope: adminpb.Index_COLLECTION, ApiScope: adminpb.Index_ANY_API}
	if !s.isSearch() {
		idx.Density = adminpb.Index_DENSE
	}
	for _, f := range s.Fields {
		pf := &adminpb.Index_IndexField{FieldPath: f.Path}
		switch f.Mode {
		case IndexAsc:
			pf.ValueMode = &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_ASCENDING}
		case IndexDesc:
			pf.ValueMode = &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_DESCENDING}
		case IndexContains:
			pf.ValueMode = &adminpb.Index_IndexField_ArrayConfig_{ArrayConfig: adminpb.Index_IndexField_CONTAINS}
		case IndexText:
			pf.ValueMode = &adminpb.Index_IndexField_SearchConfig_{SearchConfig: &adminpb.Index_IndexField_SearchConfig{
				TextSpec: &adminpb.Index_IndexField_SearchConfig_SearchTextSpec{
					IndexSpecs: []*adminpb.Index_IndexField_SearchConfig_SearchTextIndexSpec{{
						IndexType: adminpb.Index_IndexField_SearchConfig_TOKENIZED,
						MatchType: adminpb.Index_IndexField_SearchConfig_MATCH_GLOBALLY,
					}},
				},
			}}
		}
		idx.Fields = append(idx.Fields, pf)
	}
	return idx
}
