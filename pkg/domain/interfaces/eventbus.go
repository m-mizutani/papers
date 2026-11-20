package interfaces

import (
	"context"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// EventBus fans realtime events out to every subscriber of a paper on every
// instance. Delivery is best effort: subscribers detect gaps through commit
// sequence numbers and resynchronise over the HTTP API.
type EventBus interface {
	Publish(ctx context.Context, event *model.Event) error
	// Subscribe returns a channel that receives the paper's events until the
	// returned cancel function is called or ctx is done. Slow subscribers
	// may miss events; the channel is closed when the subscription ends.
	Subscribe(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, func(), error)
}
