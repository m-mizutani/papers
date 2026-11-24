package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	admin "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// Migrate brings the indexes and TTL policies of a Firestore Enterprise
// database to RequiredIndexes and TTLFields. It is idempotent: each wanted
// index is first classified against what the server lists, and only the
// classification decides what is written, so a database already in the
// wanted state sees no write at all.

// IndexStatus is the classification of one index in a migration.
type IndexStatus string

const (
	// IndexReady: a matching index exists and is READY.
	IndexReady IndexStatus = "ready"
	// IndexCreating: a matching index exists and is still being built.
	IndexCreating IndexStatus = "creating"
	// IndexCreated: no matching index existed; this run created it (or found
	// that a concurrent run had just created it).
	IndexCreated IndexStatus = "created"
	// IndexMissing: no matching index exists (dry run only).
	IndexMissing IndexStatus = "missing"
	// IndexFailed: the index needs repair, or the server and papers disagree
	// on whether it exists.
	IndexFailed IndexStatus = "failed"
	// IndexUnmanaged: an index in a managed collection group that matches
	// no RequiredIndexes entry.
	IndexUnmanaged IndexStatus = "unmanaged"
	// IndexDeleted: an unmanaged index this run deleted (prune only).
	IndexDeleted IndexStatus = "deleted"
)

// TTLStatus is the classification of one TTL policy in a migration.
type TTLStatus string

const (
	TTLOK       TTLStatus = "ok"
	TTLCreating TTLStatus = "creating"
	TTLEnabled  TTLStatus = "enabled"
	TTLMissing  TTLStatus = "missing"
	TTLFailed   TTLStatus = "failed"
)

// IndexResult is what Migrate found and did for one index.
type IndexResult struct {
	// Spec is the wanted index; nil for unmanaged and deleted indexes.
	Spec *IndexSpec
	// Name is the server's resource name, when known.
	Name string
	// Description is how the index reads: the spec, or for an unmanaged
	// index the server's definition.
	Description string
	Status      IndexStatus
	// Detail explains a failed status.
	Detail string
}

// TTLResult is what Migrate found and did for one TTL policy.
type TTLResult struct {
	CollectionGroup string
	Field           string
	Status          TTLStatus
	Detail          string
}

func (r TTLResult) String() string { return r.CollectionGroup + "." + r.Field }

// MigrateReport lists every index and TTL policy Migrate looked at.
type MigrateReport struct {
	Indexes []*IndexResult
	TTL     []*TTLResult
}

// Pending are the indexes still being built (created or creating).
func (r *MigrateReport) Pending() []*IndexResult {
	var out []*IndexResult
	for _, x := range r.Indexes {
		if (x.Status == IndexCreated || x.Status == IndexCreating) && x.Name != "" {
			out = append(out, x)
		}
	}
	return out
}

// Failed counts the indexes and TTL policies that ended as failed.
func (r *MigrateReport) Failed() int {
	n := 0
	for _, x := range r.Indexes {
		if x.Status == IndexFailed {
			n++
		}
	}
	for _, x := range r.TTL {
		if x.Status == TTLFailed {
			n++
		}
	}
	return n
}

// MigrateOptions changes what Migrate.Apply writes.
type MigrateOptions struct {
	// DryRun reads only and reports what would be created or deleted.
	DryRun bool
	// Prune deletes unmanaged indexes.
	Prune bool
}

// adminAPI is the part of the Firestore admin API a migration uses. Tests
// replace it, since the emulator has no admin API.
type adminAPI interface {
	GetDatabase(ctx context.Context, name string) (*adminpb.Database, error)
	ListIndexes(ctx context.Context, parent string) ([]*adminpb.Index, error)
	// CreateIndex starts building idx and returns its resource name, or ""
	// when the server did not tell.
	CreateIndex(ctx context.Context, parent string, idx *adminpb.Index) (string, error)
	GetIndex(ctx context.Context, name string) (*adminpb.Index, error)
	DeleteIndex(ctx context.Context, name string) error
	GetField(ctx context.Context, name string) (*adminpb.Field, error)
	// EnableTTL starts a TTL policy on the field without waiting for it.
	EnableTTL(ctx context.Context, name string) error
}

// Migrator migrates one database.
type Migrator struct {
	api    adminAPI
	closer func() error
	dbName string

	pollInitial time.Duration
	pollMax     time.Duration
}

// NewMigrator connects to the admin API of the database.
func NewMigrator(ctx context.Context, projectID, databaseID string) (*Migrator, error) {
	client, err := admin.NewFirestoreAdminClient(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create firestore admin client")
	}
	m := newMigrator(&adminClient{client: client}, projectID, databaseID)
	m.closer = client.Close
	return m, nil
}

func newMigrator(api adminAPI, projectID, databaseID string) *Migrator {
	if databaseID == "" {
		databaseID = "(default)"
	}
	return &Migrator{
		api:         api,
		dbName:      fmt.Sprintf("projects/%s/databases/%s", projectID, databaseID),
		pollInitial: time.Second,
		pollMax:     10 * time.Second,
	}
}

// Close releases the admin client.
func (m *Migrator) Close() error {
	if m.closer == nil {
		return nil
	}
	return m.closer()
}

func (m *Migrator) parent(group string) string {
	return m.dbName + "/collectionGroups/" + group
}

// Apply checks the edition, classifies every wanted index and TTL policy,
// and makes the writes the classification calls for. Failed indexes and
// policies are reported, not returned as errors; an error means the
// migration could not run.
func (m *Migrator) Apply(ctx context.Context, opts MigrateOptions) (*MigrateReport, error) {
	db, err := m.api.GetDatabase(ctx, m.dbName)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get database", goerr.V("database", m.dbName))
	}
	if db.GetDatabaseEdition() != adminpb.Database_ENTERPRISE {
		return nil, goerr.New("papers requires a Firestore Enterprise edition database",
			goerr.V("database", m.dbName), goerr.V("edition", db.GetDatabaseEdition().String()))
	}

	report := &MigrateReport{}
	var groups []string
	for _, spec := range RequiredIndexes {
		if !slices.Contains(groups, spec.CollectionGroup) {
			groups = append(groups, spec.CollectionGroup)
		}
	}
	for _, group := range groups {
		results, err := m.applyGroup(ctx, group, opts)
		if err != nil {
			return nil, err
		}
		report.Indexes = append(report.Indexes, results...)
	}
	for _, ttl := range TTLFields {
		res, err := m.applyTTL(ctx, ttl.CollectionGroup, ttl.Field, opts)
		if err != nil {
			return nil, err
		}
		report.TTL = append(report.TTL, res)
	}
	return report, nil
}

func (m *Migrator) list(ctx context.Context, group string) ([]*adminpb.Index, error) {
	parent := m.parent(group)
	all, err := m.api.ListIndexes(ctx, parent)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list indexes", goerr.V("parent", parent))
	}
	// Keep only the composite indexes of this collection group, whatever
	// else the server may return.
	prefix := parent + "/indexes/"
	var out []*adminpb.Index
	for _, idx := range all {
		if strings.HasPrefix(idx.GetName(), prefix) {
			out = append(out, idx)
		}
	}
	return out, nil
}

func (m *Migrator) applyGroup(ctx context.Context, group string, opts MigrateOptions) ([]*IndexResult, error) {
	existing, err := m.list(ctx, group)
	if err != nil {
		return nil, err
	}
	matched := map[string]bool{}
	var results []*IndexResult
	for i := range RequiredIndexes {
		spec := &RequiredIndexes[i]
		if spec.CollectionGroup != group {
			continue
		}
		res := &IndexResult{Spec: spec, Description: spec.String()}
		results = append(results, res)
		for _, e := range existing {
			if matchesSpec(*spec, e) {
				matched[e.GetName()] = true
			}
		}
		if best := bestMatch(*spec, existing); best != nil {
			res.Name = best.GetName()
			switch best.GetState() {
			case adminpb.Index_READY:
				res.Status = IndexReady
			case adminpb.Index_NEEDS_REPAIR:
				res.Status = IndexFailed
				res.Detail = "the index needs repair"
			default:
				res.Status = IndexCreating
			}
			continue
		}
		if opts.DryRun {
			res.Status = IndexMissing
			continue
		}
		if err := m.create(ctx, *spec, res); err != nil {
			return nil, err
		}
	}
	for _, e := range existing {
		if matched[e.GetName()] {
			continue
		}
		res := &IndexResult{Name: e.GetName(), Description: describeIndex(group, e), Status: IndexUnmanaged}
		results = append(results, res)
		if !opts.Prune || opts.DryRun {
			continue
		}
		if err := m.api.DeleteIndex(ctx, e.GetName()); err != nil && status.Code(err) != codes.NotFound {
			return nil, goerr.Wrap(err, "failed to delete index", goerr.V("index", e.GetName()))
		}
		res.Status = IndexDeleted
	}
	return results, nil
}

// create starts building spec. When the server says the index already
// exists (a concurrent run) or does not return its name, the group is
// listed again to find it; finding no match means the server considers an
// index equivalent that papers does not, which is reported rather than
// hidden.
func (m *Migrator) create(ctx context.Context, spec IndexSpec, res *IndexResult) error {
	name, err := m.api.CreateIndex(ctx, m.parent(spec.CollectionGroup), spec.proto())
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return goerr.Wrap(err, "failed to create index", goerr.V("index", spec.String()))
	}
	res.Status = IndexCreated
	if err == nil && name != "" {
		res.Name = name
		return nil
	}
	existing, lerr := m.list(ctx, spec.CollectionGroup)
	if lerr != nil {
		return lerr
	}
	if best := bestMatch(spec, existing); best != nil {
		res.Name = best.GetName()
		return nil
	}
	if err == nil {
		return goerr.New("created index is not listed", goerr.V("index", spec.String()))
	}
	defs := make([]string, len(existing))
	for i, e := range existing {
		defs[i] = describeIndex(spec.CollectionGroup, e)
	}
	res.Status = IndexFailed
	res.Detail = "the server reports the index exists, but no listed index matches it; listed: " + strings.Join(defs, "; ")
	return nil
}

// Wait polls the pending indexes of report until each is READY or needs
// repair, updating their status, and fails when timeout passes first.
func (m *Migrator) Wait(ctx context.Context, report *MigrateReport, timeout time.Duration) error {
	pending := report.Pending()
	deadline := time.Now().Add(timeout)
	interval := m.pollInitial
	for {
		var rest []*IndexResult
		for _, res := range pending {
			idx, err := m.api.GetIndex(ctx, res.Name)
			if err != nil {
				return goerr.Wrap(err, "failed to get index", goerr.V("index", res.Name))
			}
			switch idx.GetState() {
			case adminpb.Index_READY:
				res.Status = IndexReady
			case adminpb.Index_NEEDS_REPAIR:
				res.Status = IndexFailed
				res.Detail = "the index build failed and needs repair"
			default:
				rest = append(rest, res)
			}
		}
		pending = rest
		if len(pending) == 0 {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			names := make([]string, len(pending))
			for i, res := range pending {
				names[i] = res.Description
			}
			return goerr.New("timed out waiting for indexes to become ready",
				goerr.V("timeout", timeout.String()), goerr.V("indexes", names))
		}
		// The last sleep ends at the deadline, so the indexes are checked
		// once more before giving up.
		select {
		case <-ctx.Done():
			return goerr.Wrap(ctx.Err(), "interrupted while waiting for indexes")
		case <-time.After(min(interval, remaining)):
		}
		interval = min(interval*2, m.pollMax)
	}
}

func (m *Migrator) applyTTL(ctx context.Context, group, field string, opts MigrateOptions) (*TTLResult, error) {
	res := &TTLResult{CollectionGroup: group, Field: field}
	name := m.parent(group) + "/fields/" + field
	f, err := m.api.GetField(ctx, name)
	if err != nil && status.Code(err) != codes.NotFound {
		return nil, goerr.Wrap(err, "failed to get field", goerr.V("field", name))
	}
	if cfg := f.GetTtlConfig(); cfg != nil {
		switch cfg.GetState() {
		case adminpb.Field_TtlConfig_ACTIVE:
			res.Status = TTLOK
		case adminpb.Field_TtlConfig_NEEDS_REPAIR:
			res.Status = TTLFailed
			res.Detail = "the TTL policy needs repair"
		default:
			res.Status = TTLCreating
		}
		return res, nil
	}
	if opts.DryRun {
		res.Status = TTLMissing
		return res, nil
	}
	if err := m.api.EnableTTL(ctx, name); err != nil {
		return nil, goerr.Wrap(err, "failed to enable TTL", goerr.V("field", name))
	}
	res.Status = TTLEnabled
	return res, nil
}

// bestMatch is the existing index matching spec in the best state: READY,
// then CREATING, then NEEDS_REPAIR.
func bestMatch(spec IndexSpec, existing []*adminpb.Index) *adminpb.Index {
	rank := func(s adminpb.Index_State) int {
		switch s {
		case adminpb.Index_READY:
			return 0
		case adminpb.Index_NEEDS_REPAIR:
			return 2
		default:
			return 1
		}
	}
	var best *adminpb.Index
	for _, e := range existing {
		if matchesSpec(spec, e) && (best == nil || rank(e.GetState()) < rank(best.GetState())) {
			best = e
		}
	}
	return best
}

// matchesSpec reports whether got can serve the Native mode queries spec
// was written for. Comparing field paths and directions is not enough:
//   - the server appends __name__ in the direction of the last field (ASC
//     after a non-directional one), so got must equal the wanted fields with
//     or without exactly that __name__; one in another direction or position
//     cannot serve the query's implicit order;
//   - only ANY_API indexes serve Native queries (not Datastore or MongoDB);
//   - sparse indexes leave out documents and serve fewer queries, so ordered
//     indexes must be DENSE (UNSPECIFIED is the Enterprise default, DENSE);
//   - unique, multikey and search index options change what the index
//     does; shard count only its speed. The server fills in the default
//     search index options (textLanguage "und", textLanguageOverrideFieldPath
//     "language") on a search index created without them, so on a search
//     index those defaults count as no options;
//   - the query scope must be the spec's: collection scoped indexes serve
//     papers' collection queries, collection group ones its group queries;
//   - search fields compare their text index specs as a set.
func matchesSpec(spec IndexSpec, got *adminpb.Index) bool {
	if got.GetQueryScope() != spec.scope() || got.GetApiScope() != adminpb.Index_ANY_API {
		return false
	}
	if got.GetUnique() || got.GetMultikey() {
		return false
	}
	if opts := got.GetSearchIndexOptions(); opts != nil && (!spec.isSearch() || !defaultSearchOptions(opts)) {
		return false
	}
	if !spec.isSearch() {
		if d := got.GetDensity(); d != adminpb.Index_DENSITY_UNSPECIFIED && d != adminpb.Index_DENSE {
			return false
		}
	}
	want := spec.proto().GetFields()
	return sameFields(want, got.GetFields()) || sameFields(withImplicitName(want), got.GetFields())
}

// Default search index options: what the server reports for a search index
// created without options (the empty values mean the same).
const (
	defaultTextLanguage         = "und"
	defaultTextLanguageOverride = "language"
)

// defaultSearchOptions reports whether opts are the options the server fills
// in on a search index created without any.
func defaultSearchOptions(opts *adminpb.Index_SearchIndexOptions) bool {
	return defaultTextLanguageValue(opts.GetTextLanguage()) &&
		defaultTextLanguageOverrideValue(opts.GetTextLanguageOverrideFieldPath())
}

func defaultTextLanguageValue(v string) bool {
	return v == "" || v == defaultTextLanguage
}

func defaultTextLanguageOverrideValue(v string) bool {
	return v == "" || v == defaultTextLanguageOverride
}

// hasSearchField reports whether idx is a full-text search index.
func hasSearchField(idx *adminpb.Index) bool {
	for _, f := range idx.GetFields() {
		if _, ok := f.GetValueMode().(*adminpb.Index_IndexField_SearchConfig_); ok {
			return true
		}
	}
	return false
}

// withImplicitName is fields followed by the __name__ field the server
// appends to it.
func withImplicitName(fields []*adminpb.Index_IndexField) []*adminpb.Index_IndexField {
	order := adminpb.Index_IndexField_ASCENDING
	if n := len(fields); n > 0 {
		if o, ok := fields[n-1].GetValueMode().(*adminpb.Index_IndexField_Order_); ok {
			order = o.Order
		}
	}
	return append(slices.Clone(fields), &adminpb.Index_IndexField{
		FieldPath: "__name__", ValueMode: &adminpb.Index_IndexField_Order_{Order: order},
	})
}

func sameFields(a, b []*adminpb.Index_IndexField) bool {
	return slices.EqualFunc(a, b, sameField)
}

func sameField(a, b *adminpb.Index_IndexField) bool {
	if a.GetFieldPath() != b.GetFieldPath() {
		return false
	}
	switch av := a.GetValueMode().(type) {
	case *adminpb.Index_IndexField_Order_:
		bv, ok := b.GetValueMode().(*adminpb.Index_IndexField_Order_)
		return ok && av.Order == bv.Order
	case *adminpb.Index_IndexField_ArrayConfig_:
		bv, ok := b.GetValueMode().(*adminpb.Index_IndexField_ArrayConfig_)
		return ok && av.ArrayConfig == bv.ArrayConfig
	case *adminpb.Index_IndexField_SearchConfig_:
		bv, ok := b.GetValueMode().(*adminpb.Index_IndexField_SearchConfig_)
		return ok && sameSearch(av.SearchConfig, bv.SearchConfig)
	default:
		return false
	}
}

func sameSearch(a, b *adminpb.Index_IndexField_SearchConfig) bool {
	if a.GetGeoSpec() != nil || b.GetGeoSpec() != nil {
		return false
	}
	key := func(c *adminpb.Index_IndexField_SearchConfig) []string {
		var keys []string
		for _, s := range c.GetTextSpec().GetIndexSpecs() {
			keys = append(keys, s.GetIndexType().String()+"/"+s.GetMatchType().String())
		}
		slices.Sort(keys)
		return slices.Compact(keys)
	}
	ka, kb := key(a), key(b)
	return len(ka) > 0 && slices.Equal(ka, kb)
}

// describeIndex renders a server index the way IndexSpec.String does, with
// the attributes that make it differ from what papers creates.
func describeIndex(group string, idx *adminpb.Index) string {
	parts := make([]string, 0, len(idx.GetFields()))
	for _, f := range idx.GetFields() {
		mode := "?"
		switch v := f.GetValueMode().(type) {
		case *adminpb.Index_IndexField_Order_:
			if v.Order == adminpb.Index_IndexField_DESCENDING {
				mode = string(IndexDesc)
			} else {
				mode = string(IndexAsc)
			}
		case *adminpb.Index_IndexField_ArrayConfig_:
			mode = string(IndexContains)
		case *adminpb.Index_IndexField_SearchConfig_:
			mode = string(IndexText)
		case *adminpb.Index_IndexField_VectorConfig_:
			mode = "vector"
		}
		parts = append(parts, f.GetFieldPath()+" "+mode)
	}
	s := group + " (" + strings.Join(parts, ", ") + ")"
	var attrs []string
	if sc := idx.GetQueryScope(); sc != adminpb.Index_COLLECTION {
		attrs = append(attrs, "scope="+sc.String())
	}
	if as := idx.GetApiScope(); as != adminpb.Index_ANY_API {
		attrs = append(attrs, "api="+as.String())
	}
	if d := idx.GetDensity(); d != adminpb.Index_DENSITY_UNSPECIFIED && d != adminpb.Index_DENSE {
		attrs = append(attrs, "density="+d.String())
	}
	if idx.GetUnique() {
		attrs = append(attrs, "unique")
	}
	if idx.GetMultikey() {
		attrs = append(attrs, "multikey")
	}
	// On a search index only values other than the defaults are shown; on
	// any other index every value is.
	search := hasSearchField(idx)
	if v := idx.GetSearchIndexOptions().GetTextLanguage(); v != "" && (!search || !defaultTextLanguageValue(v)) {
		attrs = append(attrs, "textLanguage="+v)
	}
	if v := idx.GetSearchIndexOptions().GetTextLanguageOverrideFieldPath(); v != "" && (!search || !defaultTextLanguageOverrideValue(v)) {
		attrs = append(attrs, "textLanguageOverrideFieldPath="+v)
	}
	if st := idx.GetState(); st != adminpb.Index_READY {
		attrs = append(attrs, "state="+st.String())
	}
	if len(attrs) > 0 {
		s += " [" + strings.Join(attrs, ", ") + "]"
	}
	return s
}

// adminClient is adminAPI on the Firestore admin client.
type adminClient struct {
	client *admin.FirestoreAdminClient
}

func (c *adminClient) GetDatabase(ctx context.Context, name string) (*adminpb.Database, error) {
	return c.client.GetDatabase(ctx, &adminpb.GetDatabaseRequest{Name: name})
}

func (c *adminClient) ListIndexes(ctx context.Context, parent string) ([]*adminpb.Index, error) {
	it := c.client.ListIndexes(ctx, &adminpb.ListIndexesRequest{Parent: parent})
	var out []*adminpb.Index
	for {
		idx, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
}

func (c *adminClient) CreateIndex(ctx context.Context, parent string, idx *adminpb.Index) (string, error) {
	op, err := c.client.CreateIndex(ctx, &adminpb.CreateIndexRequest{Parent: parent, Index: idx})
	if err != nil {
		return "", err
	}
	// The operation is not waited on: Migrator.Wait polls the index state,
	// which also covers indexes another run created.
	meta, err := op.Metadata()
	if err != nil {
		// The index was created; its name is looked up by listing instead.
		errutil.Handle(ctx, goerr.Wrap(err, "failed to read index operation metadata"), "index name will be looked up by listing")
		return "", nil
	}
	return meta.GetIndex(), nil
}

func (c *adminClient) GetIndex(ctx context.Context, name string) (*adminpb.Index, error) {
	return c.client.GetIndex(ctx, &adminpb.GetIndexRequest{Name: name})
}

func (c *adminClient) DeleteIndex(ctx context.Context, name string) error {
	return c.client.DeleteIndex(ctx, &adminpb.DeleteIndexRequest{Name: name})
}

func (c *adminClient) GetField(ctx context.Context, name string) (*adminpb.Field, error) {
	return c.client.GetField(ctx, &adminpb.GetFieldRequest{Name: name})
}

func (c *adminClient) EnableTTL(ctx context.Context, name string) error {
	// The operation is not waited on: its wait can hang, and a TTL policy
	// being built only delays expiry.
	_, err := c.client.UpdateField(ctx, &adminpb.UpdateFieldRequest{
		Field: &adminpb.Field{Name: name, TtlConfig: &adminpb.Field_TtlConfig{}},
		// Only the TTL policy changes; without the mask the request would
		// also replace the field's index configuration.
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"ttl_config"}},
	})
	return err
}
