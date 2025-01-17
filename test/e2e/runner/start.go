package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/dashpay/tenderdash/libs/log"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/test/e2e/pkg/infra"
)

func Start(ctx context.Context, logger log.Logger, testnet *e2e.Testnet, ti infra.TestnetInfra) error {
	if len(testnet.Nodes) == 0 {
		return fmt.Errorf("no nodes in testnet")
	}

	// Nodes are already sorted by name. Sort them by name then startAt,
	// which gives the overall order startAt, mode, name.
	nodeQueue := testnet.Nodes
	sort.SliceStable(nodeQueue, func(i, j int) bool {
		a, b := nodeQueue[i], nodeQueue[j]
		switch {
		case a.Mode == b.Mode:
			return false
		case a.Mode == e2e.ModeSeed:
			return true
		case a.Mode == e2e.ModeValidator && b.Mode == e2e.ModeFull:
			return true
		}
		return false
	})

	sort.SliceStable(nodeQueue, func(i, j int) bool {
		return nodeQueue[i].StartAt < nodeQueue[j].StartAt
	})

	if nodeQueue[0].StartAt > 0 {
		return fmt.Errorf("no initial nodes in testnet")
	}

	// Start initial nodes (StartAt: 0)
	logger.Info("Starting initial network nodes...")
	for len(nodeQueue) > 0 && nodeQueue[0].StartAt == 0 {
		node := nodeQueue[0]
		nodeQueue = nodeQueue[1:]
		if err := ti.StartNode(ctx, node); err != nil {
			return err
		}

		if err := func() error {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()

			_, err := waitForNode(ctx, logger, node, 0)
			return err
		}(); err != nil {
			return err
		}
		node.HasStarted = true
		logger.Info(fmt.Sprintf("Node %v up on http://%v:%v", node.IP, node.Name, node.ProxyPort))
	}

	networkHeight := testnet.InitialHeight

	// Wait for initial height
	logger.Info("Waiting for initial height",
		"height", networkHeight,
		"nodes", len(testnet.Nodes)-len(nodeQueue),
		"pending", len(nodeQueue))

	for _, node := range nodeQueue {
		if node.StartAt > networkHeight {
			// if we're starting a node that's ahead of
			// the last known height of the network, then
			// we should make sure that the rest of the
			// network has reached at least the height
			// that this node will start at before we
			// start the node.

			logger.Info("Waiting for network to advance to height",
				"node", node.Name,
				"last_height", networkHeight,
				"waiting_for", node.StartAt,
				"size", len(testnet.Nodes)-len(nodeQueue),
				"pending", len(nodeQueue))

			networkHeight = node.StartAt

			if _, _, err := waitForHeight(ctx, testnet, networkHeight); err != nil {
				return err
			}
		}

		if err := ti.StartNode(ctx, node); err != nil {
			return err
		}

		wctx, wcancel := context.WithTimeout(ctx, 8*time.Minute)
		status, err := waitForNode(wctx, logger, node, node.StartAt)
		if err != nil {
			wcancel()
			return err
		}
		wcancel()

		node.HasStarted = true

		var lastNodeHeight int64

		// If the node is a light client, we fetch its current height
		if node.Mode == e2e.ModeLight {
			lastNodeHeight = status.LightClientInfo.LastTrustedHeight
		} else {
			lastNodeHeight = status.SyncInfo.LatestBlockHeight
		}
		logger.Info(fmt.Sprintf("Node %v up on http://%v:%v at height %v",
			node.IP, node.Name, node.ProxyPort, lastNodeHeight))
	}

	return nil
}
