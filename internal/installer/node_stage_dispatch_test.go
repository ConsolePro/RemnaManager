package installer

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestImagePinningDispatchesAgentStageTwoForBothJournalVersions(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			r, runner := managementFixture(t)
			j := provisionJob(t)
			j.Version = version
			if err := prepareNewNodeSettings(&j); err != nil {
				t.Fatal(err)
			}
			logicalStage := 2
			if version == 2 {
				logicalStage = 3
			}
			for stage := 0; stage < logicalStage; stage++ {
				j.Completed[stage] = true
			}
			originalSecret, originalKey, originalShortID := j.Secret, j.RealityKey, j.ShortID
			digest := "remnawave/node@sha256:" + strings.Repeat("a", 64)
			calls := 0
			runner.response = func(call string, input []byte) ([]byte, error) {
				calls++
				if !strings.HasPrefix(call, "ssh ") || !strings.Contains(string(input), "RM_STAGE='2'\n") {
					t.Fatalf("image pinning dispatched the wrong remote stage: %s", strings.SplitN(string(input), "\n", 5)[:4])
				}
				return []byte("RM_NODE_IMAGE=" + digest + "\n"), nil
			}
			if err := r.runNodeStage(context.Background(), &j, logicalStage); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || j.Image != digest || !j.Completed[logicalStage] {
				t.Fatal("image was not pinned", calls, j.Image)
			}
			if j.Secret != originalSecret || j.RealityKey != originalKey || j.ShortID != originalShortID {
				t.Fatal("resume regenerated saved credentials")
			}
		})
	}
}
