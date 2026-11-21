package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	admin "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Firestore Enterprise creates no index automatically and runs a query
// without a matching index as a collection scan, which is slow and billed
// per scanned document. Every query of this package is therefore listed
// here, and `papers firestore setup` creates what is missing. Adding a
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
	{CollectionGroup: colPapers, Fields: []IndexField{{"ParentID", IndexAsc}, {"Title", IndexAsc}}, Query: "ListChildren"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"Visibility", IndexAsc}, {"CreatedBy.UserID", IndexAsc}, {"Title", IndexAsc}}, Query: "ListPrivatePapers"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"CreatedBy.UserID", IndexAsc}, {"DeletedAt", IndexAsc}}, Query: "ListTrash"},
	{CollectionGroup: colPapers, Fields: []IndexField{{"DeletedAt", IndexAsc}}, Query: "ListTrashedBefore"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexAsc}}, Query: "ListBlocksRange, Tx.FirstAfter, Tx.Children"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"ParentID", IndexAsc}, {"Order", IndexDesc}}, Query: "Tx.Last"},
	{CollectionGroup: colBlocks, Fields: []IndexField{{"Type", IndexAsc}, {"Order", IndexAsc}}, Query: "ListPaperBlocks"},
	{CollectionGroup: colVersions, Fields: []IndexField{{"Number", IndexDesc}}, Query: "ListVersions"},
	{CollectionGroup: colEvents, Fields: []IndexField{{"Event.At", IndexAsc}}, Query: "ListEvents, Watch"},
	{CollectionGroup: colAttachments, Fields: []IndexField{{"SHA256", IndexAsc}}, Query: "FindAttachmentByHash"},
	{CollectionGroup: colSearch, Fields: []IndexField{{"Text", IndexText}}, Query: "Search"},
}

// TTLFields are the fields whose time value expires the document.
var TTLFields = []struct{ CollectionGroup, Field string }{
	{colSessions, "ExpiresAt"},
	{colEvents, "ExpireAt"},
}

func (s IndexSpec) proto() *adminpb.Index {
	idx := &adminpb.Index{QueryScope: adminpb.Index_COLLECTION}
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

// fieldKey describes one index field so that a wanted and an existing index
// can be compared regardless of server-filled details.
func fieldKey(f *adminpb.Index_IndexField) string {
	switch m := f.GetValueMode().(type) {
	case *adminpb.Index_IndexField_Order_:
		return f.GetFieldPath() + ":" + m.Order.String()
	case *adminpb.Index_IndexField_ArrayConfig_:
		return f.GetFieldPath() + ":" + m.ArrayConfig.String()
	case *adminpb.Index_IndexField_SearchConfig_:
		return f.GetFieldPath() + ":TEXT"
	default:
		return f.GetFieldPath() + ":?"
	}
}

// sameIndex reports whether existing serves the same queries as want. The
// server may append the document name (__name__) as the last field.
func sameIndex(want, existing *adminpb.Index) bool {
	if want.GetQueryScope() != existing.GetQueryScope() {
		return false
	}
	ef := existing.GetFields()
	if n := len(ef); n > 0 && ef[n-1].GetFieldPath() == "__name__" && len(want.GetFields()) < n {
		ef = ef[:n-1]
	}
	if len(ef) != len(want.GetFields()) {
		return false
	}
	for i, f := range want.GetFields() {
		if fieldKey(f) != fieldKey(ef[i]) {
			return false
		}
	}
	return true
}

// SetupReport is what Setup found and did.
type SetupReport struct {
	Created  []IndexSpec
	Existing []IndexSpec
	TTL      []string
}

// Setup checks that the database is a Firestore Enterprise database and
// creates the missing indexes and TTL policies. With dryRun it only reports
// what it would create. Index creation is asynchronous on the server; Setup
// starts it and returns without waiting for the build to finish.
func Setup(ctx context.Context, projectID, databaseID string, dryRun bool) (*SetupReport, error) {
	client, err := admin.NewFirestoreAdminClient(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create firestore admin client")
	}
	defer func() { _ = client.Close() }()

	dbName := fmt.Sprintf("projects/%s/databases/%s", projectID, databaseID)
	db, err := client.GetDatabase(ctx, &adminpb.GetDatabaseRequest{Name: dbName})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get database", goerr.V("database", dbName))
	}
	if db.GetDatabaseEdition() != adminpb.Database_ENTERPRISE {
		return nil, goerr.New("papers requires a Firestore Enterprise edition database",
			goerr.V("database", dbName), goerr.V("edition", db.GetDatabaseEdition().String()))
	}

	report := &SetupReport{}
	existing := map[string][]*adminpb.Index{}
	for _, spec := range RequiredIndexes {
		parent := dbName + "/collectionGroups/" + spec.CollectionGroup
		if _, ok := existing[spec.CollectionGroup]; !ok {
			list, err := listIndexes(ctx, client, parent)
			if err != nil {
				return nil, err
			}
			existing[spec.CollectionGroup] = list
		}
		want := spec.proto()
		if slices.ContainsFunc(existing[spec.CollectionGroup], func(e *adminpb.Index) bool { return sameIndex(want, e) }) {
			report.Existing = append(report.Existing, spec)
			continue
		}
		report.Created = append(report.Created, spec)
		if dryRun {
			continue
		}
		if _, err := client.CreateIndex(ctx, &adminpb.CreateIndexRequest{Parent: parent, Index: want}); err != nil {
			return nil, goerr.Wrap(err, "failed to create index", goerr.V("index", spec.String()))
		}
	}

	for _, ttl := range TTLFields {
		name := fmt.Sprintf("%s/collectionGroups/%s/fields/%s", dbName, ttl.CollectionGroup, ttl.Field)
		field, err := client.GetField(ctx, &adminpb.GetFieldRequest{Name: name})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get field", goerr.V("field", name))
		}
		if field.GetTtlConfig() != nil {
			continue
		}
		report.TTL = append(report.TTL, ttl.CollectionGroup+"."+ttl.Field)
		if dryRun {
			continue
		}
		_, err = client.UpdateField(ctx, &adminpb.UpdateFieldRequest{
			Field: &adminpb.Field{Name: name, TtlConfig: &adminpb.Field_TtlConfig{}},
			// Only the TTL policy changes; without the mask the request
			// would also replace the field's index configuration.
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"ttl_config"}},
		})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to enable TTL", goerr.V("field", name))
		}
	}
	return report, nil
}

func listIndexes(ctx context.Context, client *admin.FirestoreAdminClient, parent string) ([]*adminpb.Index, error) {
	it := client.ListIndexes(ctx, &adminpb.ListIndexesRequest{Parent: parent})
	var out []*adminpb.Index
	for {
		idx, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list indexes", goerr.V("parent", parent))
		}
		out = append(out, idx)
	}
}
