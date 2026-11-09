package server

import (
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// LinkSyncAction selects the operation applied to one link inside a
// LinkSyncMany batch.
type LinkSyncAction int

const (
	LinkSyncAdd LinkSyncAction = iota
	LinkSyncDel
	LinkSyncUpdate
)

func (a LinkSyncAction) String() string {
	switch a {
	case LinkSyncAdd:
		return "add"
	case LinkSyncDel:
		return "del"
	case LinkSyncUpdate:
		return "update"
	default:
		return "unknown"
	}
}

// LinkSyncOperation is one link operation in a LinkSyncMany batch.
type LinkSyncOperation struct {
	Action LinkSyncAction
	Config LinkConfig
}

// LinkSyncOutcome reports the result of one operation. The slice returned by
// LinkSyncMany preserves the request order, even though deletes, adds and
// updates are executed in separate phases.
type LinkSyncOutcome struct {
	Action         LinkSyncAction
	Peer1          string
	Peer2          string
	Err            error
	DurationMicros int64
}

// LinkSyncMany applies a burst of link operations under one topology lock. It
// executes deletes first, then adds, then QoS updates, reuses a single node
// netns cache and rewrites the topology file at most once at the end.
//
// A failing operation is reported in its outcome and does not abort the other
// operations. The topology file is only rewritten when sync is true and at
// least one operation was dispatched.
func (t *NetemTopologyManager) LinkSyncMany(operations []LinkSyncOperation, sync bool) ([]LinkSyncOutcome, error) {
	outcomes := make([]LinkSyncOutcome, len(operations))
	for idx := range operations {
		outcomes[idx].Action = operations[idx].Action
		outcomes[idx].Peer1 = operations[idx].Config.Peer1
		outcomes[idx].Peer2 = operations[idx].Config.Peer2
	}
	if len(operations) == 0 {
		return outcomes, nil
	}

	// Validate and deduplicate before taking the topology lock. Duplicate
	// operations would otherwise be applied in a nondeterministic order.
	groups := make([][]int, 3)
	seen := [3]map[linkKey]struct{}{}
	for idx := range seen {
		seen[idx] = make(map[linkKey]struct{})
	}
	dispatched := false

	for idx, op := range operations {
		if op.Action < LinkSyncAdd || op.Action > LinkSyncUpdate {
			outcomeErrSync(&outcomes[idx], fmt.Errorf("unknown link sync action %d", op.Action))
			continue
		}
		if op.Action == LinkSyncAdd || op.Action == LinkSyncUpdate {
			if err := op.Config.Validate(); err != nil {
				outcomeErrSync(&outcomes[idx], err)
				continue
			}
		}

		key, err := linkKeyFor(op.Config.Peer1, op.Config.Peer2)
		if err != nil {
			outcomeErrSync(&outcomes[idx], err)
			continue
		}
		if _, duplicate := seen[op.Action][key]; duplicate {
			outcomeErrSync(&outcomes[idx], fmt.Errorf("link appears more than once in the batch"))
			continue
		}
		seen[op.Action][key] = struct{}{}

		groups[op.Action] = append(groups[op.Action], idx)
		dispatched = true
	}

	if !dispatched {
		return outcomes, nil
	}

	if sync {
		t.opMu.Lock()
		defer t.opMu.Unlock()
	} else {
		t.opMu.RLock()
		defer t.opMu.RUnlock()
	}

	cache := newNodeNetnsCache()
	defer cache.closeAll()

	for _, action := range []LinkSyncAction{LinkSyncDel, LinkSyncAdd, LinkSyncUpdate} {
		indexes := groups[action]
		if len(indexes) == 0 {
			continue
		}

		group := new(errgroup.Group)
		group.SetLimit(linkCreateLimit())
		for _, idx := range indexes {
			idx := idx
			op := operations[idx]
			group.Go(func() error {
				start := time.Now()
				var err error
				switch action {
				case LinkSyncDel:
					err = t.linkDel(op.Config, cache)
				case LinkSyncAdd:
					err = t.linkAdd(op.Config, cache)
				case LinkSyncUpdate:
					err = t.linkUpdate(op.Config, cache)
				}
				outcomes[idx].Err = err
				outcomes[idx].DurationMicros = time.Since(start).Microseconds()
				return nil
			})
		}
		_ = group.Wait()
	}

	if sync {
		if err := t.SynchroniseTopology(); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

func outcomeErrSync(outcome *LinkSyncOutcome, err error) {
	outcome.Err = err
	outcome.DurationMicros = 0
}
