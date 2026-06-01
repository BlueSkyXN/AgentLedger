package pricing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type Profile struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Currency      string   `json:"currency"`
	Unit          string   `json:"unit"`
	CheckedAt     string   `json:"checked_at"`
	Defaults      Defaults `json:"defaults"`
	Sources       []Source `json:"sources"`
	Rules         []Rule   `json:"rules"`
}

type Defaults struct {
	UnknownModelPolicy   string `json:"unknown_model_policy"`
	ReasoningPolicy      string `json:"reasoning_policy"`
	CacheWriteAssumption string `json:"cache_write_assumption"`
	Confidence           string `json:"confidence"`
}

type Source struct {
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	CheckedAt string `json:"checked_at"`
}

type Rule struct {
	ID                   string    `json:"id"`
	Provider             string    `json:"provider"`
	Channel              string    `json:"channel"`
	ModelPatterns        []string  `json:"model_patterns"`
	Priority             int       `json:"priority"`
	Basis                string    `json:"basis"`
	EffectiveFrom        string    `json:"effective_from"`
	EffectiveTo          string    `json:"effective_to"`
	Condition            Condition `json:"condition"`
	Rates                Rates     `json:"rates"`
	ReasoningPolicy      string    `json:"reasoning_policy"`
	CacheWriteAssumption string    `json:"cache_write_assumption"`
	Confidence           string    `json:"confidence"`
	Notes                string    `json:"notes"`
}

type Condition struct {
	MinInputSideTokens    *int64 `json:"min_input_side_tokens"`
	RequiresObservability string `json:"requires_observability"`
}

type Rates struct {
	Input         *Rate `json:"input"`
	CachedInput   *Rate `json:"cached_input"`
	Output        *Rate `json:"output"`
	CacheCreation *Rate `json:"cache_creation"`
	CacheWrite    *Rate `json:"cache_write"`
	CacheWrite5m  *Rate `json:"cache_write_5m"`
	CacheWrite1h  *Rate `json:"cache_write_1h"`
	CacheRead     *Rate `json:"cache_read"`
	Reasoning     *Rate `json:"reasoning"`
	Request       *Rate `json:"request"`
}

type Rate struct {
	raw string
}

func (r *Rate) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		return nil
	}
	if strings.HasPrefix(text, "\"") {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		text = strings.TrimSpace(value)
	}
	if text == "" {
		text = "0"
	}
	if _, ok := new(big.Rat).SetString(text); !ok {
		return fmt.Errorf("invalid pricing rate %q", text)
	}
	r.raw = text
	return nil
}

func (r *Rate) MicroUSD(tokens int64) (int64, error) {
	if r == nil || r.raw == "" || tokens == 0 {
		return 0, nil
	}
	rat, ok := new(big.Rat).SetString(r.raw)
	if !ok {
		return 0, fmt.Errorf("invalid pricing rate %q", r.raw)
	}
	num := new(big.Int).Mul(rat.Num(), big.NewInt(tokens))
	den := rat.Denom()
	quotient, remainder := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(den) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("pricing cost overflows int64")
	}
	return quotient.Int64(), nil
}

func (r *Rate) Float64() float64 {
	if r == nil || r.raw == "" {
		return 0
	}
	value, _ := new(big.Rat).SetString(r.raw)
	f, _ := value.Float64()
	return f
}

func (p *Profile) Validate() error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("unsupported pricing schema_version %d", p.SchemaVersion)
	}
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("pricing profile id is required")
	}
	if strings.TrimSpace(p.Currency) == "" {
		return fmt.Errorf("pricing profile currency is required")
	}
	if strings.TrimSpace(p.Unit) != "usd_per_1m_tokens" {
		return fmt.Errorf("unsupported pricing unit %q", p.Unit)
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("pricing profile must contain at least one rule")
	}
	seen := make(map[string]bool, len(p.Rules))
	for _, rule := range p.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("pricing rule id is required")
		}
		if seen[rule.ID] {
			return fmt.Errorf("duplicate pricing rule id %q", rule.ID)
		}
		seen[rule.ID] = true
		if len(rule.ModelPatterns) == 0 {
			return fmt.Errorf("pricing rule %q must include model_patterns", rule.ID)
		}
		if rule.Rates.Input == nil && rule.Rates.Output == nil && rule.Rates.CachedInput == nil && rule.Rates.CacheRead == nil && rule.Rates.CacheCreation == nil && rule.Rates.CacheWrite == nil && rule.Rates.CacheWrite5m == nil {
			return fmt.Errorf("pricing rule %q must include at least one token rate", rule.ID)
		}
	}
	return nil
}
