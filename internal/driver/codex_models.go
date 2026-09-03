package driver

import "strings"

type codexModelEntry struct {
	PublicID      string
	ContextWindow int
	Advertise     bool
	Family        string
	Pricing       codexModelPricing
}

type codexModelPricing struct {
	Input       float64
	Output      float64
	CacheRead   float64
	CacheCreate float64
}

var (
	codexGPT6AstraPricing  = codexModelPricing{Input: 20, Output: 100, CacheRead: 2, CacheCreate: 25}
	codexGPT56SolPricing   = codexModelPricing{Input: 10, Output: 60, CacheRead: 1.00, CacheCreate: 12.50}
	codexGPT56TerraPricing = codexModelPricing{Input: 5, Output: 30, CacheRead: 0.50, CacheCreate: 6.25}
	codexGPT56LunaPricing  = codexModelPricing{Input: 2, Output: 12, CacheRead: 0.20, CacheCreate: 2.50}
	codexGPT55Pricing      = codexModelPricing{Input: 12.50, Output: 75, CacheRead: 1.25, CacheCreate: 12.50}
	codexGPT54Pricing      = codexModelPricing{Input: 5, Output: 30, CacheRead: 0.50, CacheCreate: 5}
	codexGPT54MiniPricing  = codexModelPricing{Input: 1.50, Output: 9, CacheRead: 0.15, CacheCreate: 1.50}
	codexDefaultPricing    = codexModelPricing{Input: 2, Output: 8, CacheRead: 0.50, CacheCreate: 2}
)

var codexModelEntries = []codexModelEntry{
	{PublicID: "gpt-5.6-sol", ContextWindow: 1050000, Advertise: true, Pricing: codexGPT56SolPricing},
	{PublicID: "gpt-5.6-terra", ContextWindow: 1050000, Advertise: true, Pricing: codexGPT56TerraPricing},
	{PublicID: "gpt-5.6-luna", ContextWindow: 400000, Advertise: true, Pricing: codexGPT56LunaPricing},
	{PublicID: "gpt-6-astra", ContextWindow: 1050000, Advertise: true, Pricing: codexGPT6AstraPricing},
	{PublicID: "gpt-5.6", ContextWindow: 1050000, Advertise: false, Pricing: codexGPT56SolPricing},
	{PublicID: "gpt-5.5", ContextWindow: 400000, Advertise: true, Pricing: codexGPT55Pricing},
	{PublicID: "gpt-5.4", ContextWindow: 1050000, Advertise: true, Pricing: codexGPT54Pricing},
	{PublicID: "gpt-5.4-mini", ContextWindow: 1050000, Advertise: true, Pricing: codexGPT54MiniPricing},
	{PublicID: "gpt-5.3-codex", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.3-codex-spark", ContextWindow: 128000, Advertise: true, Family: "bengalfox", Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.2-codex", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.2", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.1-codex-max", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.1-codex", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.1-codex-mini", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5.1", ContextWindow: 400000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5-codex", ContextWindow: 192000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5-codex-mini", ContextWindow: 192000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "gpt-5", ContextWindow: 192000, Advertise: true, Pricing: codexDefaultPricing},
	{PublicID: "codex-1", ContextWindow: 192000, Advertise: true, Pricing: codexDefaultPricing},
}

func codexSupportedModels() []Model {
	models := make([]Model, 0, len(codexModelEntries))
	for _, entry := range codexModelEntries {
		if !entry.Advertise {
			continue
		}
		models = append(models, Model{
			ID:            entry.PublicID,
			Object:        "model",
			Created:       1709164800,
			OwnedBy:       "openai",
			ContextWindow: entry.ContextWindow,
		})
	}
	return models
}

func codexModelEntryForID(model string) (codexModelEntry, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	for _, entry := range codexModelEntries {
		if normalized == entry.PublicID {
			return entry, true
		}
	}
	return codexModelEntry{}, false
}

func codexPricingForModel(model string) codexModelPricing {
	if entry, ok := codexModelEntryForID(model); ok {
		return entry.Pricing
	}
	return legacyCodexPassthroughPricing(model)
}

func legacyCodexPassthroughPricing(model string) codexModelPricing {
	// Failure model: raw passthrough slugs outside the advertised Codex catalog
	// can still succeed upstream. Keep the old coarse estimator for those until
	// Codex request model validation becomes the single accepted boundary.
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "o3"):
		return codexModelPricing{Input: 2, Output: 8, CacheRead: 0.50, CacheCreate: 2}
	case strings.Contains(lower, "o4-mini"):
		return codexModelPricing{Input: 1.10, Output: 4.40, CacheRead: 0.275, CacheCreate: 1.10}
	case strings.Contains(lower, "codex-mini"):
		return codexModelPricing{Input: 1.50, Output: 6, CacheRead: 0.375, CacheCreate: 1.50}
	case strings.Contains(lower, "4.1-nano"):
		return codexModelPricing{Input: 0.10, Output: 0.40, CacheRead: 0.025, CacheCreate: 0.10}
	case strings.Contains(lower, "4.1-mini"):
		return codexModelPricing{Input: 0.40, Output: 1.60, CacheRead: 0.10, CacheCreate: 0.40}
	case strings.Contains(lower, "4.1"):
		return codexModelPricing{Input: 2, Output: 8, CacheRead: 0.50, CacheCreate: 2}
	default:
		return codexDefaultPricing
	}
}

func (p codexModelPricing) cost(usage *Usage) float64 {
	if usage == nil {
		return 0
	}
	uncachedInput := usage.InputTokens - usage.CacheReadTokens - usage.CacheCreateTokens
	if uncachedInput < 0 {
		uncachedInput = 0
	}
	return (float64(uncachedInput)*p.Input + float64(usage.OutputTokens)*p.Output +
		float64(usage.CacheReadTokens)*p.CacheRead + float64(usage.CacheCreateTokens)*p.CacheCreate) / 1_000_000
}
