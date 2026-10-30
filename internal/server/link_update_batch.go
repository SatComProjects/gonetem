package server

import (
	"errors"
	"time"

	"github.com/mroy31/gonetem/internal/link"
	"golang.org/x/sync/errgroup"
)

// LinkUpdateOutcome reports the result of one link update inside a batch. The
// slice returned by LinkUpdateMany preserves the request order.
type LinkUpdateOutcome struct {
	Peer1          string
	Peer2          string
	Err            error
	DurationMicros int64
}

type linkQoSChange struct {
	link     *NetemLink
	ifName   string
	node     INetemNode
	op       link.QoSOperation
	hasTbf   *bool
	hasNetem *bool
	result   link.QoSOperationResult
	err      error
}

type linkBatchEntry struct {
	link    *NetemLink
	outcome *LinkUpdateOutcome
	start   time.Time
	changes []*linkQoSChange
}

type linkQoSGroup struct {
	node    INetemNode
	changes []*linkQoSChange
}

// LinkUpdateMany applies several link QoS updates in one operation. It takes
// the topology write lock once, groups qdisc work by node, enters each node
// namespace once, and opens one rtnetlink session per node. Per-link failures
// are returned in the outcome slice and do not abort the other links.
func (t *NetemTopologyManager) LinkUpdateMany(configs []LinkConfig, sync bool) ([]LinkUpdateOutcome, error) {
	outcomes := make([]LinkUpdateOutcome, len(configs))
	for idx := range configs {
		outcomes[idx].Peer1 = configs[idx].Peer1
		outcomes[idx].Peer2 = configs[idx].Peer2
	}
	if len(configs) == 0 {
		return outcomes, nil
	}

	// A batch is a single logical operation. Taking the write side once avoids
	// one lock acquisition per link and keeps the config snapshot consistent
	// while the batch is being applied.
	t.opMu.Lock()
	defer t.opMu.Unlock()

	entries := make([]*linkBatchEntry, 0, len(configs))
	locked := make([]*NetemLink, 0, len(configs))
	lockedSet := make(map[*NetemLink]bool, len(configs))
	defer func() {
		for _, l := range locked {
			l.mu.Unlock()
		}
	}()

	for idx := range configs {
		cfg := configs[idx]
		entry := &linkBatchEntry{
			outcome: &outcomes[idx],
			start:   time.Now(),
		}

		if err := cfg.Validate(); err != nil {
			outcomeErr(entry, err)
			continue
		}

		t.mu.RLock()
		l, _, err := t.getLinkLocked(cfg.Peer1, cfg.Peer2)
		t.mu.RUnlock()
		if err != nil {
			outcomeErr(entry, err)
			continue
		}
		if lockedSet[l] {
			outcomeErr(entry, errors.New("link appears more than once in the batch"))
			continue
		}

		l.mu.Lock()
		locked = append(locked, l)
		lockedSet[l] = true
		entry.link = l

		t.mu.Lock()
		oldPeer1 := l.Config.GetPeer1QoS()
		oldPeer2 := l.Config.GetPeer2QoS()
		applyLinkUpdateConfig(l, cfg)
		newPeer1 := l.Config.GetPeer1QoS()
		newPeer2 := l.Config.GetPeer2QoS()
		running := t.running
		t.mu.Unlock()

		if running {
			if change := buildLinkQoSChange(l, 1, oldPeer1, newPeer1); change != nil {
				entry.changes = append(entry.changes, change)
			}
			if change := buildLinkQoSChange(l, 2, oldPeer2, newPeer2); change != nil {
				entry.changes = append(entry.changes, change)
			}
		}

		entries = append(entries, entry)
	}

	if err := t.applyLinkQoSGroups(entries); err != nil {
		return outcomes, err
	}

	for _, entry := range entries {
		for _, change := range entry.changes {
			if change.err != nil {
				entry.outcome.Err = errors.Join(entry.outcome.Err, change.err)
				continue
			}
			if change.result.Err != nil {
				entry.outcome.Err = errors.Join(entry.outcome.Err, change.result.Err)
				continue
			}
			*change.hasTbf = change.result.HasTbf
			*change.hasNetem = change.result.HasNetem
		}
		entry.outcome.DurationMicros = time.Since(entry.start).Microseconds()
	}

	if sync {
		if err := t.SynchroniseTopology(); err != nil {
			return outcomes, err
		}
	}

	return outcomes, nil
}

func outcomeErr(entry *linkBatchEntry, err error) {
	entry.outcome.Err = err
	entry.outcome.DurationMicros = time.Since(entry.start).Microseconds()
}

func buildLinkQoSChange(l *NetemLink, peer int, old, next QoSConfig) *linkQoSChange {
	if old == next {
		return nil
	}

	var (
		node     INetemNode
		ifIndex  int
		hasTbf   *bool
		hasNetem *bool
	)
	if peer == 1 {
		node, ifIndex = l.Peer1.Node, l.Peer1.IfIndex
		hasTbf, hasNetem = &l.HasPeer1Tbf, &l.HasPeer1Netem
	} else {
		node, ifIndex = l.Peer2.Node, l.Peer2.IfIndex
		hasTbf, hasNetem = &l.HasPeer2Tbf, &l.HasPeer2Netem
	}

	reset, changeTbf, changeNetem, changed := qosDiff(old, next, *hasTbf, *hasNetem)
	if !changed {
		return nil
	}

	ifName := node.GetInterfaceName(ifIndex)
	return &linkQoSChange{
		link:   l,
		ifName: ifName,
		node:   node,
		op: link.QoSOperation{
			IfName:      ifName,
			QoS:         toLinkQoS(next),
			Reset:       reset,
			ChangeTbf:   changeTbf,
			ChangeNetem: changeNetem,
		},
		hasTbf:   hasTbf,
		hasNetem: hasNetem,
	}
}

func (t *NetemTopologyManager) applyLinkQoSGroups(entries []*linkBatchEntry) error {
	groups := make(map[string]*linkQoSGroup)
	for _, entry := range entries {
		for _, change := range entry.changes {
			key := change.node.GetName()
			group := groups[key]
			if group == nil {
				group = &linkQoSGroup{node: change.node}
				groups[key] = group
			}
			group.changes = append(group.changes, change)
		}
	}
	if len(groups) == 0 {
		return nil
	}

	cache := newNodeNetnsCache()
	defer cache.closeAll()

	groupErrs := new(errgroup.Group)
	groupErrs.SetLimit(linkCreateLimit())
	for _, group := range groups {
		group := group
		groupErrs.Go(func() error {
			ns, err := cache.get(group.node)
			if err != nil {
				for _, change := range group.changes {
					change.err = err
				}
				return nil
			}

			ops := make([]link.QoSOperation, len(group.changes))
			for idx := range group.changes {
				ops[idx] = group.changes[idx].op
			}
			apply := t.qosBatchApplier
			if apply == nil {
				apply = link.ApplyQoSBatch
			}
			results, err := apply(ns, ops)
			if err != nil {
				for _, change := range group.changes {
					change.err = err
				}
				return nil
			}
			if len(results) != len(group.changes) {
				err := errors.New("invalid qdisc batch result length")
				for _, change := range group.changes {
					change.err = err
				}
				return nil
			}
			for idx := range group.changes {
				group.changes[idx].result = results[idx]
			}
			return nil
		})
	}
	_ = groupErrs.Wait()
	return nil
}
