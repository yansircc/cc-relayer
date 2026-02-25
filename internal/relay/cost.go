package relay

import "strings"

type usageData struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CacheReadTokens   int `json:"cache_read_input_tokens"`
	CacheCreateTokens int `json:"cache_creation_input_tokens"`
}

// calcCost computes the estimated cost in USD based on model and token counts.
// Pricing per 1M tokens (as of 2025):
//   - sonnet: in=$3, out=$15, cache_read=$0.30, cache_create=$3.75
//   - opus: in=$15, out=$75, cache_read=$1.50, cache_create=$18.75
//   - haiku: in=$0.80, out=$4, cache_read=$0.08, cache_create=$1
func calcCost(model string, input, output, cacheRead, cacheCreate int) float64 {
	lower := strings.ToLower(model)
	var inPrice, outPrice, cacheReadPrice, cacheCreatePrice float64
	switch {
	case strings.Contains(lower, "opus"):
		inPrice, outPrice, cacheReadPrice, cacheCreatePrice = 15, 75, 1.50, 18.75
	case strings.Contains(lower, "haiku"):
		inPrice, outPrice, cacheReadPrice, cacheCreatePrice = 0.80, 4, 0.08, 1
	default: // sonnet and unknown
		inPrice, outPrice, cacheReadPrice, cacheCreatePrice = 3, 15, 0.30, 3.75
	}
	return (float64(input)*inPrice + float64(output)*outPrice +
		float64(cacheRead)*cacheReadPrice + float64(cacheCreate)*cacheCreatePrice) / 1_000_000
}
