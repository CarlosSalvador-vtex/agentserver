package codexexecgateway

import (
	"context"
	"time"

	sdkpkg "github.com/agentserver/agentserver/internal/codexexecgateway/sdk"
)

// sdkConnectedAdapter bridges the gateway's existing *Store + *ConnRegistry
// into the sdk.ConnectedLister interface.  It is constructed once in
// NewServer and embedded in the sdk.Server struct field Registry.
//
// Implementation mirrors handlers.Connected but returns the slice directly
// instead of writing JSON, eliminating an HTTP round-trip.
type sdkConnectedAdapter struct {
	store    *Store
	registry *ConnRegistry
}

// Connected satisfies sdk.ConnectedLister.  It returns the intersection of
// (workspace's bound executors) ∩ (currently-connected exe_ids) in the shape
// the SDK package expects.
func (a sdkConnectedAdapter) Connected(ctx context.Context, wsID string) ([]sdkpkg.ConnectedExecutor, error) {
	ids := a.registry.ConnectedIDs()
	rows, err := a.store.ConnectedExecutorsForWorkspace(ctx, wsID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]sdkpkg.ConnectedExecutor, 0, len(rows))
	for _, e := range rows {
		var lastSeen string
		if e.LastSeenAt != nil {
			lastSeen = e.LastSeenAt.UTC().Format(time.RFC3339)
		}
		out = append(out, sdkpkg.ConnectedExecutor{
			ExeID:      e.ExeID,
			Name:       e.Name,
			IsDefault:  e.IsDefault,
			LastSeenAt: lastSeen,
		})
	}
	return out, nil
}
