package wails

// ResizeResult is what a resize ASKED FOR, handed back so the interface can
// say it.
//
// NOT WHAT HAPPENED. The kubelet decides that — it may change the cgroup
// immediately, defer the change until the node has room, or call it
// infeasible — and the answer arrives as a condition on the pod, which the
// assessment reads and reports as a finding. A result that claimed the resize
// was applied would be claiming the kubelet's answer before it gave one.
//
// Restarts is the fact worth carrying back: it is decided by the container's
// own resizePolicy, which most people have never read, and it is the
// difference between changing a number and restarting a database.
type ResizeResult struct {
	Container     string `json:"container"`
	CPURequest    string `json:"cpuRequest"`
	CPULimit      string `json:"cpuLimit"`
	MemoryRequest string `json:"memoryRequest"`
	MemoryLimit   string `json:"memoryLimit"`
	Restarts      bool   `json:"restarts"`
	// RestartReason names the resource whose policy forces the restart —
	// "cpu" or "memory" — for the sentence the interface shows. Empty when
	// nothing restarts.
	RestartReason string `json:"restartReason"`
}
