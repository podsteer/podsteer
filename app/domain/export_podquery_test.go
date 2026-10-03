package domain

// RunPodQueryForTest exposes the query's verdict over prepared rows — the
// indices it keeps, in order, and the chip counts — so the parity test can
// run the frontend's fixture rows without building Pods from them.
func RunPodQueryForTest(rows []PodQueryRow, q PodQuery, collation CollationKey) ([]int, map[PodStatusChip]int) {
	result := runPodQuery(rows, q, collation)
	return result.order, result.chipCounts
}

// CompareTextForTest orders two strings as the table's sort does.
func CompareTextForTest(a, b string, collation CollationKey) int {
	return compareTextKeys(newTextKey(a, collation), newTextKey(b, collation), string(collation("0")))
}
