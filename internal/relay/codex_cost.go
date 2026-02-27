package relay

import "strings"

// calcCodexCost computes the estimated cost in USD for Codex models.
// Pricing per 1M tokens (approximate):
//   - o3: in=$2, out=$8, cached_in=$0.50
//   - o4-mini: in=$1.10, out=$4.40, cached_in=$0.275
//   - codex-mini: in=$1.50, out=$6, cached_in=$0.375
//   - gpt-4.1: in=$2, out=$8, cached_in=$0.50
//   - gpt-4.1-mini: in=$0.40, out=$1.60, cached_in=$0.10
//   - gpt-4.1-nano: in=$0.10, out=$0.40, cached_in=$0.025
func calcCodexCost(model string, input, output, cacheRead int) float64 {
	lower := strings.ToLower(model)
	var inPrice, outPrice, cacheReadPrice float64
	switch {
	case strings.Contains(lower, "o3"):
		inPrice, outPrice, cacheReadPrice = 2, 8, 0.50
	case strings.Contains(lower, "o4-mini"):
		inPrice, outPrice, cacheReadPrice = 1.10, 4.40, 0.275
	case strings.Contains(lower, "codex-mini"):
		inPrice, outPrice, cacheReadPrice = 1.50, 6, 0.375
	case strings.Contains(lower, "4.1-nano"):
		inPrice, outPrice, cacheReadPrice = 0.10, 0.40, 0.025
	case strings.Contains(lower, "4.1-mini"):
		inPrice, outPrice, cacheReadPrice = 0.40, 1.60, 0.10
	case strings.Contains(lower, "4.1"):
		inPrice, outPrice, cacheReadPrice = 2, 8, 0.50
	default:
		inPrice, outPrice, cacheReadPrice = 2, 8, 0.50
	}
	return (float64(input)*inPrice + float64(output)*outPrice +
		float64(cacheRead)*cacheReadPrice) / 1_000_000
}
