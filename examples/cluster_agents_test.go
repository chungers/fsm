package main

import (
	"fmt"
	"testing"
	"time"

	. "github.com/orkestr8/fsm"

	"github.com/stretchr/testify/require"
)

// This example here assumes:
// 1. A cluster is formed by nodes where each node is an instance running an agent
// 2. Agents running on physical instances join together
// 3. Cluster membership is basically agents running properly.
// 4. When agent is down, that physical instance is rmoved.

const (
	agentReady Signal = iota
	agentDown
	agentGone
	instanceOK
	instanceGone
	timeout
	reap
)

const (
	start                  Index = iota
	matchedInstance              // has vm information, waiting to match to agent_node
	matchedAgent                 // has agent_node information, waiting to match to vm
	clusterNode                  // has matching agent_node and vm information
	clusterNodeReady             // ready as cluster node
	clusterNodeDown              // unavailable as cluster node
	pendingInstanceDestroy       // vm needs to be removedInstance (instance destroy)
	removedInstance              // instance is deleted
	done                         // terminal
)

type gc struct {
	ch chan FSM
	op string
}

func (gc gc) do(i FSM) error {
	fmt.Println(gc.op, i)
	gc.ch <- i
	return nil
}

func TestClusterCases(t *testing.T) {

	pollInterval := 100 * time.Millisecond

	noData := Tick(10)
	agentJoin := Tick(5)
	waitDescribeInstances := Tick(5)
	waitBeforeInstanceDestroy := Tick(3)
	waitBeforeReprovision := Tick(10) // wait before we reprovision a new instance to fix a Down node
	waitBeforeCleanup := Tick(10)

	// actions
	agentRm := &gc{
		ch: make(chan FSM, 1),
		op: "agentRm",
	}

	instanceDestroy := &gc{
		ch: make(chan FSM, 1),
		op: "instanceDestroy",
	}

	machines, err := Define(
		State{
			Index: start,
			TTL:   Expiry{TTL: noData, Raise: timeout},
			Transitions: map[Signal]Index{
				agentReady: matchedAgent,
				agentDown:  matchedAgent,
				instanceOK: matchedInstance,
				timeout:    removedInstance, // nothing happened... cleanup
			},
		},
		State{
			Index: matchedInstance,
			TTL:   Expiry{TTL: agentJoin, Raise: agentGone},
			Transitions: map[Signal]Index{
				agentReady:   clusterNode,
				agentDown:    clusterNode,
				agentGone:    pendingInstanceDestroy,
				instanceGone: removedInstance,
			},
			Actions: map[Signal]Action{
				instanceGone: instanceDestroy.do,
			},
		},
		State{
			Index: pendingInstanceDestroy,
			TTL:   Expiry{TTL: waitBeforeInstanceDestroy, Raise: reap},
			Transitions: map[Signal]Index{
				agentReady:   clusterNode, // late joiner
				agentDown:    clusterNode,
				instanceGone: removedInstance,
				reap:         removedInstance,
			},
			Actions: map[Signal]Action{
				instanceGone: instanceDestroy.do,
				reap:         instanceDestroy.do,
			},
		},
		State{
			Index: matchedAgent,
			TTL:   Expiry{TTL: waitDescribeInstances, Raise: instanceGone},
			Transitions: map[Signal]Index{
				instanceOK:   clusterNode,
				instanceGone: removedInstance,
				agentGone:    removedInstance, // could be agent rm'd out of band
			},
			Actions: map[Signal]Action{
				instanceGone: agentRm.do,
			},
		},
		State{
			Index: clusterNode,
			Transitions: map[Signal]Index{
				agentReady:   clusterNodeReady,
				agentDown:    clusterNodeDown,
				agentGone:    matchedInstance,
				instanceGone: matchedAgent,
			},
		},
		State{
			Index: clusterNodeReady,
			Transitions: map[Signal]Index{
				agentDown:    clusterNodeDown,
				agentGone:    matchedInstance,
				instanceGone: matchedAgent,
			},
		},
		State{
			Index: clusterNodeDown,
			TTL:   Expiry{TTL: waitBeforeReprovision, Raise: agentGone},
			Transitions: map[Signal]Index{
				agentReady:   clusterNodeReady,
				agentGone:    pendingInstanceDestroy,
				instanceGone: matchedAgent,
			},
		},
		State{
			Index: removedInstance, // after we removed the instance, we can still have unmatched node
			TTL:   Expiry{TTL: waitBeforeCleanup, Raise: timeout},
			Transitions: map[Signal]Index{
				agentDown: done,
				timeout:   done,
			},
			Actions: map[Signal]Action{
				agentDown: agentRm.do,
			},
		},
		State{
			Index: done, // deleted state is terminal. this will be garbage collected
		},
	)
	require.NoError(t, err)

	options := DefaultOptions()
	options.StateNames = map[Index]string{
		start:                  "START",
		matchedInstance:        "FOUND_INSTANCE",
		matchedAgent:           "FOUND_AGENT",
		clusterNode:            "CLUSTER_NODE",
		clusterNodeReady:       "READY",
		clusterNodeDown:        "DOWN",
		pendingInstanceDestroy: "PENDING_INSTNACE_DESTROY",
		removedInstance:        "REMOVED_INSTANCE",
		done:                   "DONE",
	}
	options.SignalNames = map[Signal]string{
		agentReady:   "agent-node-ready",
		agentDown:    "agent-node-down",
		agentGone:    "agent-node-gone",
		instanceOK:   "instance-ok",
		instanceGone: "instance-gone",
		timeout:      "timeout",
		reap:         "reap",
	}

	clock := Wall(time.Tick(pollInterval))
	clock.Start()

	require.NoError(t, machines.Run(clock, options))

	defer func() {
		machines.Done()
	}()

	mustAlloc := func(s Index) FSM {
		f, err := machines.New(s)
		require.NoError(t, err)
		return f
	}

	runner := map[string]FSM{
		"case1": mustAlloc(start),
	}

	{
		// case 1 - orphaned ex-leader node, a unmatched "Down" node where instance was removed and deletion observed.
		// add new instance in start state
		// we found the node via agent node ls and it's in ready state
		require.NoError(t, runner["case1"].Signal(agentReady))

		// we found the vm via plugin describe and it's in ok state
		require.NoError(t, runner["case1"].Signal(instanceOK))

		// another agent node ls, and it's still in ready
		require.NoError(t, runner["case1"].Signal(agentReady))

		// node does a cluster demote and leave -- agent node ls shows Down
		require.NoError(t, runner["case1"].Signal(agentDown))

		// Down is unavailable
		require.Equal(t, clusterNodeDown, runner["case1"].State())

		// the vm is deleted -- diff of successive 'instance describe' calls show it's gone
		require.NoError(t, runner["case1"].Signal(instanceGone))

		// instance is gone but node is there
		require.Equal(t, matchedAgent, runner["case1"].State())

		// wait a bit longer and no news from future instance describes
		time.Sleep(time.Duration(waitDescribeInstances+1) * pollInterval)

		// should be deleted
		require.Equal(t, removedInstance, runner["case1"].State())

		// we should see the node getting removed.
		gone := <-agentRm.ch

		require.Equal(t, removedInstance, gone.State())
		require.Equal(t, runner["case1"].ID(), gone.ID())

		// wait a bit and it will advance to done -- then we can clean up
		time.Sleep(time.Duration(waitBeforeCleanup+1) * pollInterval)

		require.Equal(t, done, runner["case1"].State())

		// periodically clean up the deleted instances
		delete(runner, "case1")
	}

	{
		// case2 - node fails to join
		runner["case2"] = mustAlloc(start)

		// we found the vm via plugin describe and it's in ok state
		require.NoError(t, runner["case2"].Signal(instanceOK))

		// there's a limit on agentJoin timeout
		time.Sleep(time.Duration(agentJoin+1) * pollInterval)

		// waiting to be deleted... unless the cluster node shows up in time!
		require.Equal(t, pendingInstanceDestroy, runner["case2"].State())

		// we should see the node getting removed.
		gone := <-instanceDestroy.ch

		require.Equal(t, removedInstance, gone.State())
		require.Equal(t, runner["case2"].ID(), gone.ID())

		// wait a bit and it will advance to done -- then we can clean up
		time.Sleep(time.Duration(waitBeforeCleanup+1) * pollInterval)

		require.Equal(t, done, runner["case2"].State())

		// periodically clean up the deleted instances
		delete(runner, "case2")
	}

	{
		// case3 - node / engine goes offline
		// in this case we have to do both agent node rm *and* instance destroy

		runner["case3"] = mustAlloc(start)

		// we found the vm via plugin describe and it's in ok state
		require.NoError(t, runner["case3"].Signal(instanceOK))

		// we found the engine status via agent node ls and it's in Ready state
		require.NoError(t, runner["case3"].Signal(agentReady))

		// we are now matched
		require.Equal(t, clusterNode, runner["case3"].State())

		// agent node ls gives ready again
		require.NoError(t, runner["case3"].Signal(agentReady))

		time.Sleep(5 * pollInterval) // after a while

		// a working ready cluster node
		require.Equal(t, clusterNodeReady, runner["case3"].State())

		// the node goes offline -- engine disappeared / network partition
		require.NoError(t, runner["case3"].Signal(agentDown))

		// now we are in a down state
		require.Equal(t, clusterNodeDown, runner["case3"].State())

		// we still see the matching instance
		require.NoError(t, runner["case3"].Signal(instanceOK))

		// we are still in the Down state
		require.Equal(t, clusterNodeDown, runner["case3"].State())

		// after some wait
		time.Sleep(time.Duration(waitBeforeReprovision+1) * pollInterval)

		// the instance should be ready to be destroyed
		require.Equal(t, pendingInstanceDestroy, runner["case3"].State())

		// after a while, the engine still doesn't come back on within the limit...
		time.Sleep(time.Duration(waitBeforeInstanceDestroy+1) * pollInterval)

		// we should see the instance getting removed.
		gone := <-instanceDestroy.ch

		require.Equal(t, removedInstance, gone.State())
		require.Equal(t, runner["case3"].ID(), gone.ID())

		// at this point, the instance is gone... but we still have a agent node ls entry in Down state
		// so another agent node ls shows this

		require.NoError(t, runner["case3"].Signal(agentDown))

		// we should see the node getting removed.
		gone2 := <-agentRm.ch

		// final state
		require.Equal(t, done, gone2.State())

		// clean up
		delete(runner, "case3")

	}

	{
		// case4 - rouge node -- added outside of our control
		// this is when a agent node shows up and we have no idea where that's from

		runner["case4"] = mustAlloc(start)

		// we found the engine status via agent node ls and it's in Ready state
		require.NoError(t, runner["case4"].Signal(agentReady))

		// now wait for an instance to show up from instance describe
		require.Equal(t, matchedAgent, runner["case4"].State())

		// after some wait
		time.Sleep(time.Duration(waitDescribeInstances+1) * pollInterval)

		require.Equal(t, removedInstance, runner["case4"].State())

		// we should see the node get removed
		gone := <-agentRm.ch

		require.Equal(t, removedInstance, gone.State())
		require.Equal(t, runner["case4"].ID(), gone.ID())

		// now the node is gone, but we don't have any more instance information because
		// this instance wasn't adding by us
		time.Sleep(time.Duration(waitBeforeCleanup+1) * pollInterval)

		// final state
		require.Equal(t, done, gone.State())

		// clean up
		delete(runner, "case4")

	}

}
