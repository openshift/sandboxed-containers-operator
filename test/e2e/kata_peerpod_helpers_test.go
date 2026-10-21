package kata

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

// dsJSON builds a daemonset document for daemonSetRolledOut.
func dsJSON(generation, observed, desired, updated, ready, available int) string {
	return fmt.Sprintf(`{
		"metadata": {"generation": %d},
		"status": {
			"observedGeneration": %d,
			"desiredNumberScheduled": %d,
			"updatedNumberScheduled": %d,
			"numberReady": %d,
			"numberAvailable": %d
		}
	}`, generation, observed, desired, updated, ready, available)
}

func TestDaemonSetRolledOut(t *testing.T) {
	const baseline = "3"

	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			// The operator has not reacted to the change yet. The daemonset is
			// perfectly healthy, which is why the generation has to be checked
			// against a baseline taken before the change.
			name: "not reconciled yet",
			json: dsJSON(3, 3, 2, 2, 2, 2),
			want: false,
		},
		{
			// maxUnavailable 1 over two nodes: both pods report ready, but one
			// still belongs to the previous revision.
			name: "mid rollout with every pod ready",
			json: dsJSON(4, 4, 2, 1, 2, 2),
			want: false,
		},
		{
			name: "new revision rolled out",
			json: dsJSON(4, 4, 2, 2, 2, 2),
			want: true,
		},
		{
			name: "spec written but not yet observed",
			json: dsJSON(4, 3, 2, 2, 2, 2),
			want: false,
		},
		{
			// A pod of the new revision is scheduled on every node but one is
			// not available yet, e.g. still pulling or failing to mount.
			name: "updated pod not available",
			json: dsJSON(4, 4, 2, 2, 1, 1),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := daemonSetRolledOut(tc.json, baseline)
			if got != tc.want {
				t.Errorf("daemonSetRolledOut() = %v, want %v (%s)", got, tc.want, reason)
			}
			if reason == "" {
				t.Error("daemonSetRolledOut() returned an empty reason")
			}
		})
	}
}

// The condition this replaces was a generation different from the pre-test
// baseline plus numberReady == desiredNumberScheduled. Pin down that it
// accepts the mid-rollout state daemonSetRolledOut rejects.
func TestMidRolloutSatisfiesNumberReady(t *testing.T) {
	midRollout := dsJSON(4, 4, 2, 1, 2, 2)

	ready := gjson.Get(midRollout, "status.numberReady").Int()
	desired := gjson.Get(midRollout, "status.desiredNumberScheduled").Int()
	if ready != desired {
		t.Fatalf("fixture is not representative: numberReady=%d desiredNumberScheduled=%d", ready, desired)
	}

	if rolled, reason := daemonSetRolledOut(midRollout, "3"); rolled {
		t.Fatalf("daemonSetRolledOut accepted a mid-rollout daemonset: %s", reason)
	}
}
