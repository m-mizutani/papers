package firestore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"github.com/m-mizutani/gt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const testDB = "projects/p/databases/(default)"

func parentOf(group string) string { return testDB + "/collectionGroups/" + group }

// fakeAdmin is an in-memory admin API that behaves like the server: it
// appends __name__ to ordered indexes, names indexes, and counts calls.
type fakeAdmin struct {
	edition adminpb.Database_DatabaseEdition
	indexes map[string][]*adminpb.Index // by parent
	fields  map[string]*adminpb.Field
	// listExtra is returned by ListIndexes in addition to indexes.
	listExtra map[string][]*adminpb.Index
	// states is the sequence of states GetIndex reports for an index; the
	// last one repeats. Without one, a building index is READY at the first
	// GetIndex.
	states map[string][]adminpb.Index_State
	// onCreate, when set, replaces CreateIndex.
	onCreate func(parent string, idx *adminpb.Index) (string, error)
	calls    map[string]int
	listed   []string
	nextID   int
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{
		edition:   adminpb.Database_ENTERPRISE,
		indexes:   map[string][]*adminpb.Index{},
		fields:    map[string]*adminpb.Field{},
		listExtra: map[string][]*adminpb.Index{},
		states:    map[string][]adminpb.Index_State{},
		calls:     map[string]int{},
	}
}

// serverForm is idx as the server returns it once created.
func serverForm(idx *adminpb.Index, name string, state adminpb.Index_State) *adminpb.Index {
	out := proto.Clone(idx).(*adminpb.Index)
	out.Name = name
	out.State = state
	if out.GetDensity() == adminpb.Index_DENSITY_UNSPECIFIED && !isSearchIndex(out) {
		out.Density = adminpb.Index_DENSE
	}
	if !isSearchIndex(out) {
		out.Fields = withImplicitName(out.Fields)
	}
	return out
}

func isSearchIndex(idx *adminpb.Index) bool {
	for _, f := range idx.GetFields() {
		if _, ok := f.GetValueMode().(*adminpb.Index_IndexField_SearchConfig_); ok {
			return true
		}
	}
	return false
}

func (f *fakeAdmin) add(group string, idx *adminpb.Index, state adminpb.Index_State) *adminpb.Index {
	f.nextID++
	parent := parentOf(group)
	s := serverForm(idx, fmt.Sprintf("%s/indexes/id%d", parent, f.nextID), state)
	f.indexes[parent] = append(f.indexes[parent], s)
	return s
}

// addAll makes every required index exist in state and every TTL policy in
// ttl.
func (f *fakeAdmin) addAll(state adminpb.Index_State, ttl adminpb.Field_TtlConfig_State) {
	for _, spec := range RequiredIndexes {
		f.add(spec.CollectionGroup, spec.proto(), state)
	}
	for _, t := range TTLFields {
		name := parentOf(t.CollectionGroup) + "/fields/" + t.Field
		f.fields[name] = &adminpb.Field{Name: name, TtlConfig: &adminpb.Field_TtlConfig{State: ttl}}
	}
}

func (f *fakeAdmin) find(name string) *adminpb.Index {
	for _, list := range f.indexes {
		for _, idx := range list {
			if idx.GetName() == name {
				return idx
			}
		}
	}
	return nil
}

func (f *fakeAdmin) GetDatabase(_ context.Context, name string) (*adminpb.Database, error) {
	f.calls["GetDatabase"]++
	return &adminpb.Database{Name: name, DatabaseEdition: f.edition}, nil
}

func (f *fakeAdmin) ListIndexes(_ context.Context, parent string) ([]*adminpb.Index, error) {
	f.calls["ListIndexes"]++
	f.listed = append(f.listed, parent)
	return append(append([]*adminpb.Index{}, f.indexes[parent]...), f.listExtra[parent]...), nil
}

func (f *fakeAdmin) CreateIndex(_ context.Context, parent string, idx *adminpb.Index) (string, error) {
	f.calls["CreateIndex"]++
	if f.onCreate != nil {
		return f.onCreate(parent, idx)
	}
	group := strings.TrimPrefix(parent, testDB+"/collectionGroups/")
	return f.add(group, idx, adminpb.Index_CREATING).GetName(), nil
}

func (f *fakeAdmin) GetIndex(_ context.Context, name string) (*adminpb.Index, error) {
	f.calls["GetIndex"]++
	idx := f.find(name)
	if idx == nil {
		return nil, status.Error(codes.NotFound, "no index")
	}
	if seq := f.states[name]; len(seq) > 0 {
		idx.State = seq[0]
		if len(seq) > 1 {
			f.states[name] = seq[1:]
		}
	} else if idx.GetState() == adminpb.Index_CREATING {
		idx.State = adminpb.Index_READY // builds finish at the first look by default
	}
	return idx, nil
}

func (f *fakeAdmin) DeleteIndex(_ context.Context, name string) error {
	f.calls["DeleteIndex"]++
	for parent, list := range f.indexes {
		for i, idx := range list {
			if idx.GetName() == name {
				f.indexes[parent] = append(list[:i:i], list[i+1:]...)
				return nil
			}
		}
	}
	return status.Error(codes.NotFound, "no index")
}

func (f *fakeAdmin) GetField(_ context.Context, name string) (*adminpb.Field, error) {
	f.calls["GetField"]++
	if fl, ok := f.fields[name]; ok {
		return fl, nil
	}
	return &adminpb.Field{Name: name}, nil
}

func (f *fakeAdmin) EnableTTL(_ context.Context, name string) error {
	f.calls["EnableTTL"]++
	f.fields[name] = &adminpb.Field{Name: name, TtlConfig: &adminpb.Field_TtlConfig{State: adminpb.Field_TtlConfig_CREATING}}
	return nil
}

func newTestMigrator(f *fakeAdmin) *Migrator {
	m := newMigrator(f, "p", "")
	m.pollInitial = time.Millisecond
	m.pollMax = 2 * time.Millisecond
	return m
}

func statuses(r *MigrateReport) map[IndexStatus]int {
	out := map[IndexStatus]int{}
	for _, x := range r.Indexes {
		out[x.Status]++
	}
	return out
}

func ttlStatuses(r *MigrateReport) map[TTLStatus]int {
	out := map[TTLStatus]int{}
	for _, x := range r.TTL {
		out[x.Status]++
	}
	return out
}

func resultFor(r *MigrateReport, query string) *IndexResult {
	for _, x := range r.Indexes {
		if x.Spec != nil && x.Spec.Query == query {
			return x
		}
	}
	return nil
}

func writes(f *fakeAdmin) int {
	return f.calls["CreateIndex"] + f.calls["DeleteIndex"] + f.calls["EnableTTL"]
}

func specByQuery(t *testing.T, query string) IndexSpec {
	t.Helper()
	for _, s := range RequiredIndexes {
		if s.Query == query {
			return s
		}
	}
	t.Fatalf("no spec for %s", query)
	return IndexSpec{}
}

func TestMigrateCreatesEverythingOnEmptyDatabase(t *testing.T) {
	// T1
	f := newFakeAdmin()
	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, f.calls["CreateIndex"], len(RequiredIndexes))
	gt.Equal(t, f.calls["EnableTTL"], len(TTLFields))
	gt.Equal(t, statuses(r)[IndexCreated], len(RequiredIndexes))
	gt.Equal(t, ttlStatuses(r)[TTLEnabled], len(TTLFields))
	gt.Equal(t, len(r.Pending()), len(RequiredIndexes))
	for _, x := range r.Indexes {
		gt.NotEqual(t, x.Name, "")
	}
	gt.Equal(t, f.calls["GetIndex"], 0) // T7: Apply alone never waits
}

func TestMigrateIsIdempotent(t *testing.T) {
	// T2, T5 (the server form carries __name__)
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, writes(f), 0)
	gt.Equal(t, statuses(r)[IndexReady], len(RequiredIndexes))
	gt.Equal(t, ttlStatuses(r)[TTLOK], len(TTLFields))
	gt.Equal(t, r.Failed(), 0)
	gt.Equal(t, len(r.Pending()), 0)
}

func TestMigrateSecondRunAfterFirst(t *testing.T) {
	f := newFakeAdmin()
	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.NoError(t, m.Wait(context.Background(), r, time.Second))
	before := writes(f)
	r2, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, writes(f), before)
	gt.Equal(t, statuses(r2)[IndexReady], len(RequiredIndexes))
}

func TestMigrateDoesNotRecreateBuildingIndex(t *testing.T) {
	// T3
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	jobs := resultIndex(f, "jobs")
	jobs.State = adminpb.Index_CREATING
	f.states[jobs.GetName()] = []adminpb.Index_State{adminpb.Index_READY}

	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, f.calls["CreateIndex"], 0)
	res := resultFor(r, "ListRunnableJobs")
	gt.Equal(t, res.Status, IndexCreating)
	gt.Equal(t, len(r.Pending()), 1)
	gt.NoError(t, m.Wait(context.Background(), r, time.Second))
	gt.Equal(t, f.calls["GetIndex"], 1)
	gt.Equal(t, res.Status, IndexReady)
}

func resultIndex(f *fakeAdmin, group string) *adminpb.Index {
	return f.indexes[parentOf(group)][0]
}

func TestMigrateAlreadyExistsFromConcurrentRun(t *testing.T) {
	// T4, T31 (match found)
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	parent := parentOf(colJobs)
	f.indexes[parent] = nil
	f.onCreate = func(p string, idx *adminpb.Index) (string, error) {
		// Another run created it between our list and our create.
		f.add(colJobs, idx, adminpb.Index_CREATING)
		return "", status.Error(codes.AlreadyExists, "index already exists")
	}
	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListRunnableJobs")
	gt.Equal(t, res.Status, IndexCreated)
	gt.Equal(t, res.Name, f.indexes[parent][0].GetName())
	gt.Equal(t, r.Failed(), 0)
}

func TestMigrateAlreadyExistsWithoutMatch(t *testing.T) {
	// T31 (no match): the server treats a sparse index as the same one.
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	parent := parentOf(colJobs)
	f.indexes[parent][0].Density = adminpb.Index_SPARSE_ANY
	f.onCreate = func(string, *adminpb.Index) (string, error) {
		return "", status.Error(codes.AlreadyExists, "index already exists")
	}
	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListRunnableJobs")
	gt.Equal(t, res.Status, IndexFailed)
	gt.S(t, res.Detail).Contains("density=SPARSE_ANY")
	gt.Equal(t, r.Failed(), 1)
}

func TestMigrateCreateWithoutNameListsAgain(t *testing.T) {
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	f.onCreate = func(_ string, idx *adminpb.Index) (string, error) {
		f.add(colTokens, idx, adminpb.Index_CREATING)
		return "", nil
	}
	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListTokens")
	gt.Equal(t, res.Status, IndexCreated)
	gt.Equal(t, res.Name, f.indexes[parentOf(colTokens)][0].GetName())
}

func TestMigrateWaitsUntilReady(t *testing.T) {
	// T6
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListTokens")
	f.states[res.Name] = []adminpb.Index_State{adminpb.Index_CREATING, adminpb.Index_CREATING, adminpb.Index_READY}
	gt.NoError(t, m.Wait(context.Background(), r, time.Second))
	gt.Equal(t, f.calls["GetIndex"], 3)
	gt.Equal(t, res.Status, IndexReady)
}

func TestMigrateWaitReportsRepairDuringBuild(t *testing.T) {
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListTokens")
	f.states[res.Name] = []adminpb.Index_State{adminpb.Index_NEEDS_REPAIR}
	gt.NoError(t, m.Wait(context.Background(), r, time.Second))
	gt.Equal(t, res.Status, IndexFailed)
	gt.Equal(t, r.Failed(), 1)
}

func TestMigrateWaitTimesOut(t *testing.T) {
	// T10
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	m := newTestMigrator(f)
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	f.states[resultFor(r, "ListTokens").Name] = []adminpb.Index_State{adminpb.Index_CREATING}
	err = m.Wait(context.Background(), r, 5*time.Millisecond)
	gt.Error(t, err)
	gt.S(t, fmt.Sprintf("%+v", err)).Contains("tokens (UserID asc)")
}

func TestMigrateReportsUnmanagedIndexes(t *testing.T) {
	// T8, T9
	extra := &adminpb.Index{QueryScope: adminpb.Index_COLLECTION, Fields: []*adminpb.Index_IndexField{
		{FieldPath: "Title", ValueMode: &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_ASCENDING}},
	}}
	t.Run("report only", func(t *testing.T) {
		f := newFakeAdmin()
		f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
		f.add(colPapers, extra, adminpb.Index_READY)
		r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
		gt.NoError(t, err)
		gt.Equal(t, statuses(r)[IndexUnmanaged], 1)
		gt.Equal(t, f.calls["DeleteIndex"], 0)
	})
	t.Run("prune", func(t *testing.T) {
		f := newFakeAdmin()
		f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
		f.add(colPapers, extra, adminpb.Index_READY)
		r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{Prune: true})
		gt.NoError(t, err)
		gt.Equal(t, statuses(r)[IndexDeleted], 1)
		gt.Equal(t, f.calls["DeleteIndex"], 1)
		wantPapers := 0
		for _, s := range RequiredIndexes {
			if s.CollectionGroup == colPapers {
				wantPapers++
			}
		}
		gt.Equal(t, len(f.indexes[parentOf(colPapers)]), wantPapers)
		for _, p := range f.listed {
			found := false
			for _, s := range RequiredIndexes {
				if p == parentOf(s.CollectionGroup) {
					found = true
				}
			}
			gt.True(t, found).Describef("listed unmanaged group %s", p)
		}
	})
}

func TestMigrateDryRun(t *testing.T) {
	// T11
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	f.indexes[parentOf(colAttachments)] = nil
	f.add(colPapers, &adminpb.Index{QueryScope: adminpb.Index_COLLECTION, Fields: []*adminpb.Index_IndexField{
		{FieldPath: "Title", ValueMode: &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_ASCENDING}},
	}}, adminpb.Index_READY)
	delete(f.fields, parentOf(colEvents)+"/fields/ExpireAt")

	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{DryRun: true, Prune: true})
	gt.NoError(t, err)
	gt.Equal(t, writes(f), 0)
	gt.Equal(t, statuses(r)[IndexMissing], 2)
	gt.Equal(t, statuses(r)[IndexUnmanaged], 1)
	gt.Equal(t, ttlStatuses(r)[TTLMissing], 1)
}

func TestMigrateNeedsRepair(t *testing.T) {
	// T12
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	resultIndex(f, colJobs).State = adminpb.Index_NEEDS_REPAIR
	f.indexes[parentOf(colTokens)] = nil
	f.fields[parentOf(colEvents)+"/fields/ExpireAt"].TtlConfig.State = adminpb.Field_TtlConfig_NEEDS_REPAIR

	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, resultFor(r, "ListRunnableJobs").Status, IndexFailed)
	gt.Equal(t, resultFor(r, "ListTokens").Status, IndexCreated)
	gt.Equal(t, r.Failed(), 2)
	gt.Equal(t, f.calls["CreateIndex"], 1)
}

func TestMigrateRequiresEnterprise(t *testing.T) {
	// T13
	f := newFakeAdmin()
	f.edition = adminpb.Database_STANDARD
	_, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.Error(t, err)
	gt.Equal(t, f.calls["ListIndexes"], 0)
}

func TestMigrateTTLBeingBuilt(t *testing.T) {
	// T14
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_CREATING)
	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	gt.Equal(t, ttlStatuses(r)[TTLCreating], len(TTLFields))
	gt.Equal(t, f.calls["EnableTTL"], 0)
}

func TestMigrateIgnoresForeignListEntriesAndPicksBest(t *testing.T) {
	// T32
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	tokens := parentOf(colTokens)
	ready := f.indexes[tokens][0]
	broken := proto.Clone(ready).(*adminpb.Index)
	broken.Name = tokens + "/indexes/broken"
	broken.State = adminpb.Index_NEEDS_REPAIR
	f.indexes[tokens] = []*adminpb.Index{broken, ready}
	foreign := proto.Clone(ready).(*adminpb.Index)
	foreign.Name = parentOf(colUsers) + "/indexes/other"
	unnamed := proto.Clone(ready).(*adminpb.Index)
	unnamed.Name = ""
	f.listExtra[tokens] = []*adminpb.Index{foreign, unnamed}

	r, err := newTestMigrator(f).Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListTokens")
	gt.Equal(t, res.Status, IndexReady)
	gt.Equal(t, res.Name, ready.GetName())
	gt.Equal(t, statuses(r)[IndexUnmanaged], 0)
}

func ordered(path string, o adminpb.Index_IndexField_Order) *adminpb.Index_IndexField {
	return &adminpb.Index_IndexField{FieldPath: path, ValueMode: &adminpb.Index_IndexField_Order_{Order: o}}
}

const (
	asc  = adminpb.Index_IndexField_ASCENDING
	desc = adminpb.Index_IndexField_DESCENDING
)

func TestMatchesSpec(t *testing.T) {
	last := specByQuery(t, "Tx.Last")                          // blocks (ParentID asc, Order desc)
	children := specByQuery(t, "ListChildren, ListChildrenOf") // papers (ParentID asc, Title asc)
	search := specByQuery(t, "Search")

	server := func(spec IndexSpec, mod func(*adminpb.Index)) *adminpb.Index {
		idx := serverForm(spec.proto(), "n", adminpb.Index_READY)
		if mod != nil {
			mod(idx)
		}
		return idx
	}

	t.Run("T24 every required index matches its server form", func(t *testing.T) {
		for _, spec := range RequiredIndexes {
			gt.True(t, matchesSpec(spec, server(spec, nil))).Describef("%s", spec)
			gt.True(t, matchesSpec(spec, spec.proto())).Describef("%s as requested", spec)
		}
	})
	t.Run("T25 implicit __name__ follows the last field", func(t *testing.T) {
		got := server(last, nil)
		gt.Equal(t, got.Fields[2].GetFieldPath(), "__name__")
		gt.Equal(t, got.Fields[2].GetOrder(), desc)
		gt.True(t, matchesSpec(last, got))
		gt.True(t, matchesSpec(search, server(search, nil))) // no __name__ on search
	})
	t.Run("T26 __name__ in another direction or position", func(t *testing.T) {
		gt.False(t, matchesSpec(children, server(children, func(i *adminpb.Index) {
			i.Fields[2] = ordered("__name__", desc)
		})))
		gt.False(t, matchesSpec(children, server(children, func(i *adminpb.Index) {
			i.Fields = []*adminpb.Index_IndexField{ordered("ParentID", asc), ordered("__name__", asc), ordered("Title", asc)}
		})))
		gt.False(t, matchesSpec(children, server(children, func(i *adminpb.Index) {
			i.Fields = []*adminpb.Index_IndexField{ordered("Title", asc), ordered("ParentID", asc), ordered("__name__", asc)}
		})))
	})
	t.Run("T27 api scope", func(t *testing.T) {
		for _, s := range []adminpb.Index_ApiScope{adminpb.Index_MONGODB_COMPATIBLE_API, adminpb.Index_DATASTORE_MODE_API} {
			gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.ApiScope = s })))
		}
	})
	t.Run("T28 density", func(t *testing.T) {
		gt.True(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Density = adminpb.Index_DENSITY_UNSPECIFIED })))
		gt.True(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Density = adminpb.Index_DENSE })))
		gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Density = adminpb.Index_SPARSE_ALL })))
		gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Density = adminpb.Index_SPARSE_ANY })))
		gt.True(t, matchesSpec(search, server(search, func(i *adminpb.Index) { i.Density = adminpb.Index_SPARSE_ANY })))
	})
	t.Run("T29 unique, multikey, search options, shard count", func(t *testing.T) {
		gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Unique = true })))
		gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.Multikey = true })))
		gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) {
			i.SearchIndexOptions = &adminpb.Index_SearchIndexOptions{}
		})))
		gt.True(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.ShardCount = 4 })))
	})
	t.Run("query scope", func(t *testing.T) {
		for _, s := range []adminpb.Index_QueryScope{adminpb.Index_COLLECTION_GROUP, adminpb.Index_COLLECTION_RECURSIVE} {
			gt.False(t, matchesSpec(last, server(last, func(i *adminpb.Index) { i.QueryScope = s })))
		}
	})
	t.Run("T30 search specs", func(t *testing.T) {
		textSpec := func(i *adminpb.Index) *adminpb.Index_IndexField_SearchConfig {
			return i.Fields[0].GetSearchConfig()
		}
		gt.True(t, matchesSpec(search, server(search, func(i *adminpb.Index) {
			ts := textSpec(i).TextSpec
			ts.IndexSpecs = append([]*adminpb.Index_IndexField_SearchConfig_SearchTextIndexSpec{proto.Clone(ts.IndexSpecs[0]).(*adminpb.Index_IndexField_SearchConfig_SearchTextIndexSpec)}, ts.IndexSpecs...)
		})))
		gt.False(t, matchesSpec(search, server(search, func(i *adminpb.Index) {
			textSpec(i).TextSpec.IndexSpecs[0].MatchType = adminpb.Index_IndexField_SearchConfig_TEXT_MATCH_TYPE_UNSPECIFIED
		})))
		gt.False(t, matchesSpec(search, server(search, func(i *adminpb.Index) {
			textSpec(i).GeoSpec = &adminpb.Index_IndexField_SearchConfig_SearchGeoSpec{}
		})))
		gt.False(t, matchesSpec(search, server(search, func(i *adminpb.Index) {
			textSpec(i).TextSpec.IndexSpecs = nil
		})))
		gt.False(t, matchesSpec(search, server(search, func(i *adminpb.Index) {
			i.Fields[0].ValueMode = &adminpb.Index_IndexField_Order_{Order: asc}
		})))
	})
}

func TestMigrateWaitChecksAtTheDeadline(t *testing.T) {
	// A build that finishes within the timeout but after the last whole
	// interval is still seen as ready.
	f := newFakeAdmin()
	f.addAll(adminpb.Index_READY, adminpb.Field_TtlConfig_ACTIVE)
	f.indexes[parentOf(colTokens)] = nil
	m := newTestMigrator(f)
	m.pollInitial = time.Hour
	m.pollMax = time.Hour
	r, err := m.Apply(context.Background(), MigrateOptions{})
	gt.NoError(t, err)
	res := resultFor(r, "ListTokens")
	f.states[res.Name] = []adminpb.Index_State{adminpb.Index_CREATING, adminpb.Index_READY}
	gt.NoError(t, m.Wait(context.Background(), r, 20*time.Millisecond))
	gt.Equal(t, res.Status, IndexReady)
	gt.Equal(t, f.calls["GetIndex"], 2)
}
