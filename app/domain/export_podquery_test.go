package domain

// RunPodQueryForTest exposes the query's verdict over prepared rows — the
// indices it keeps, in order, and the chip counts — so the parity test can
// run the frontend's fixture rows without building Pods from them.
func RunPodQueryForTest(rows []PodQueryRow, q PodQuery, weight RuneWeight) ([]int, map[PodStatusChip]int) {
	result := runPodQuery(rows, q, weight)
	return result.order, result.chipCounts
}
