package k8s

// Tests for decoding a kubelet's Summary API response.
//
// Worth testing at this level because the shape is not a Kubernetes type this
// code compiles against — it is decoded from JSON into a hand-written struct,
// so a field the kubelet renames or omits produces a silent zero rather than a
// compile error. A silent zero here reads as "this node's disk is empty",
// which is the most dangerous wrong answer the overview could give.

import (
	"encoding/json"
	"testing"
)

// decode is what nodeSummary does once the bytes are in hand.
func decode(t *testing.T, raw string) summaryResponse {
	t.Helper()

	var summary summaryResponse
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("decoding the summary: %v", err)
	}
	return summary
}

func TestSummaryDecodesBothFilesystems(t *testing.T) {
	t.Parallel()

	summary := decode(t, `{
	  "node": {
	    "nodeName": "node-1",
	    "fs": {"capacityBytes": 100, "usedBytes": 40, "availableBytes": 60},
	    "runtime": {"imageFs": {"capacityBytes": 200, "usedBytes": 150, "availableBytes": 50}}
	  },
	  "pods": [{"podRef": {"name": "ignored"}}]
	}`)

	nodefs := summary.Node.Fs.toFilesystem()
	if nodefs.UsedBytes != 40 || nodefs.CapacityBytes != 100 {
		t.Errorf("nodefs = %+v, want 40 of 100", nodefs)
	}
	if got := nodefs.Percent(); got != 40 {
		t.Errorf("nodefs percent = %v, want 40", got)
	}

	imagefs := summary.Node.Runtime.ImageFs.toFilesystem()
	if imagefs.UsedBytes != 150 || imagefs.CapacityBytes != 200 {
		t.Errorf("imagefs = %+v, want 150 of 200", imagefs)
	}
}

// Used is preferred to capacity-minus-available, because on a filesystem with
// reserved blocks the two disagree and used is what the kubelet evicts on.
func TestSummaryPrefersUsedOverAvailable(t *testing.T) {
	t.Parallel()

	summary := decode(t, `{"node":{"fs":{"capacityBytes":100,"usedBytes":70,"availableBytes":25}}}`)

	if got := summary.Node.Fs.toFilesystem().UsedBytes; got != 70 {
		t.Errorf("used = %d, want the reported 70 rather than 100-25", got)
	}
}

// Older kubelets, and some runtimes, report only what is left.
func TestSummaryFallsBackToAvailable(t *testing.T) {
	t.Parallel()

	summary := decode(t, `{"node":{"fs":{"capacityBytes":100,"availableBytes":25}}}`)

	if got := summary.Node.Fs.toFilesystem().UsedBytes; got != 75 {
		t.Errorf("used = %d, want capacity minus available", got)
	}
}

// A response missing the parts we want must produce nothing, not zero. The
// distinction is the whole reason NodeFilesystems carries Measured.
func TestSummaryWithoutFilesystemsIsEmptyNotZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "no fs at all", raw: `{"node":{"nodeName":"node-1"}}`},
		{name: "fs without a size", raw: `{"node":{"fs":{"usedBytes":40}}}`},
		{name: "runtime without an imagefs", raw: `{"node":{"runtime":{}}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			summary := decode(t, test.raw)
			if got := summary.Node.Fs.toFilesystem(); !got.IsZero() {
				t.Errorf("nodefs = %+v, want nothing", got)
			}
			if summary.Node.Runtime != nil {
				if got := summary.Node.Runtime.ImageFs.toFilesystem(); !got.IsZero() {
					t.Errorf("imagefs = %+v, want nothing", got)
				}
			}
		})
	}
}

// A zero-size filesystem must not read as 0% full.
func TestPercentOfAnUnknownSizeIsZeroNotADivisionByZero(t *testing.T) {
	t.Parallel()

	summary := decode(t, `{"node":{"fs":{"capacityBytes":0,"usedBytes":0}}}`)

	if got := summary.Node.Fs.toFilesystem().Percent(); got != 0 {
		t.Errorf("percent = %v, want 0", got)
	}
}
