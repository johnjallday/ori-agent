package ci

import (
	"os/exec"
	"testing"
)

// Keep Python selection/aggregation regressions inside the normal unit gate.
func TestUnitShardSelectionAndAggregation(t *testing.T) {
	command := exec.CommandContext(t.Context(), "python3", "../unit-shards.test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unit shard contract: %v\n%s", err, output)
	}
}
