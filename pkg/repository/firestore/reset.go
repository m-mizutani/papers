package firestore

import (
	"context"
	"errors"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/papers/pkg/utils/safe"
)

// resetFlushEvery bounds how many deletes wait in the BulkWriter (and how
// many jobs are held in memory) before they are sent and checked.
const resetFlushEvery = 500

// ResetCount is how many documents Reset deleted under one root collection,
// its subcollections included.
type ResetCount struct {
	Collection string
	Documents  int
}

// ResetReport is what Reset deleted.
type ResetReport struct {
	Collections []ResetCount
	Total       int
}

// Reset deletes every document of the database: every root collection and,
// depth first, every subcollection below it, children before their parent.
// Documents that exist only as the parent of subcollections are visited but
// not counted. Indexes and TTL policies are database configuration and are
// kept. Reset asks nothing: confirming is the caller's job.
func Reset(ctx context.Context, projectID, databaseID string) (*ResetReport, error) {
	if projectID == "" {
		return nil, goerr.New("firestore project ID is required")
	}
	if databaseID == "" {
		databaseID = firestore.DefaultDatabaseID
	}
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create firestore client", goerr.V("project_id", projectID), goerr.V("database_id", databaseID))
	}
	defer safe.Close(ctx, client)

	d := &resetter{bw: client.BulkWriter(ctx)}
	defer d.bw.End()

	report := &ResetReport{}
	cols := client.Collections(ctx)
	for {
		col, err := cols.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list root collections")
		}
		before := d.deleted
		if err := d.collection(ctx, col); err != nil {
			return nil, err
		}
		if err := d.flush(); err != nil {
			return nil, err
		}
		n := d.deleted - before
		report.Collections = append(report.Collections, ResetCount{Collection: col.ID, Documents: n})
		report.Total += n
	}
	return report, nil
}

type resetter struct {
	bw      *firestore.BulkWriter
	jobs    []*firestore.BulkWriterJob
	deleted int
}

func (d *resetter) collection(ctx context.Context, col *firestore.CollectionRef) error {
	// DocumentRefs also returns documents that have no fields of their own
	// but hold subcollections (identities/{provider}).
	docs := col.DocumentRefs(ctx)
	for {
		doc, err := docs.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return goerr.Wrap(err, "failed to list documents", goerr.V("collection", col.Path))
		}
		subs := doc.Collections(ctx)
		for {
			sub, err := subs.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return goerr.Wrap(err, "failed to list subcollections", goerr.V("document", doc.Path))
			}
			if err := d.collection(ctx, sub); err != nil {
				return err
			}
		}
		// Exists makes a delete of a document without fields fail, which is
		// how those are told apart and left uncounted.
		job, err := d.bw.Delete(doc, firestore.Exists)
		if err != nil {
			return goerr.Wrap(err, "failed to enqueue delete", goerr.V("document", doc.Path))
		}
		d.jobs = append(d.jobs, job)
		if len(d.jobs) >= resetFlushEvery {
			if err := d.flush(); err != nil {
				return err
			}
		}
	}
}

func (d *resetter) flush() error {
	d.bw.Flush()
	for _, job := range d.jobs {
		if _, err := job.Results(); err != nil {
			// A document without fields fails the Exists precondition;
			// servers report that as NotFound or FailedPrecondition.
			if c := status.Code(err); c == codes.NotFound || c == codes.FailedPrecondition {
				continue
			}
			return goerr.Wrap(err, "failed to delete document")
		}
		d.deleted++
	}
	d.jobs = d.jobs[:0]
	return nil
}
