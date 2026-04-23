package tool

// truncateOutput truncates a string to a given length and appends a truncation notice.
func truncateOutput(output string, limit int) string {
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "\n... output truncated ..."
}
