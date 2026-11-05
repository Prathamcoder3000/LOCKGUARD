package consensus

import (
	"errors"
	"fmt"
)

var (
	// ErrNotLeader is returned when an operation requiring leader authority is invoked on a non-leader.
	ErrNotLeader = errors.New("node is not the cluster leader")

	// ErrQuorumLoss is returned when a Raft commit fails due to lack of majority quorum.
	ErrQuorumLoss = errors.New("raft quorum loss: commit failed")

	// ErrClusterNotBootstrapped is returned if the cluster has not been initialized.
	ErrClusterNotBootstrapped = errors.New("cluster is not bootstrapped")

	// ErrNodeClosed is returned when operating on a shutdown Raft node.
	ErrNodeClosed = errors.New("raft node is closed")

	// ErrTimeout is returned when a consensus operation times out.
	ErrTimeout = errors.New("raft operation timed out")
)

// NotLeaderError embeds leader hint details to allow client redirection.
type NotLeaderError struct {
	LeaderID      string `json:"leader_id,omitempty"`
	LeaderAddress string `json:"leader_address,omitempty"`
}

func (e *NotLeaderError) Error() string {
	if e.LeaderAddress != "" {
		return fmt.Sprintf("%s: current leader is %s at %s", ErrNotLeader.Error(), e.LeaderID, e.LeaderAddress)
	}
	if e.LeaderID != "" {
		return fmt.Sprintf("%s: current leader is %s", ErrNotLeader.Error(), e.LeaderID)
	}
	return fmt.Sprintf("%s (no leader elected)", ErrNotLeader.Error())
}

func (e *NotLeaderError) Unwrap() error {
	return ErrNotLeader
}

