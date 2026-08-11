package model

import "encoding/json"

// MarshalJSON provides a final serialization boundary for the private probe
// payout destination. Older in-memory records may still contain that retained
// output, but its address and script must never reach JSONL or an API payload.
func (o Observation) MarshalJSON() ([]byte, error) {
	type observationAlias Observation
	redacted := observationAlias(o)
	if len(o.CoinbaseOutputs) > 0 {
		redacted.CoinbaseOutputs = make([]CoinbaseOutput, 0, len(o.CoinbaseOutputs))
		for _, output := range o.CoinbaseOutputs {
			if output.Worker {
				continue
			}
			redacted.CoinbaseOutputs = append(redacted.CoinbaseOutputs, output)
		}
	}
	return json.Marshal(redacted)
}

// UnmarshalJSON keeps version 1-7 JSONL readable after later schema additions.
// Version 3 replaced contest-oriented names with block/report terminology.
func (o *Observation) UnmarshalJSON(data []byte) error {
	type observationAlias Observation
	aux := struct {
		*observationAlias
		LegacyBlockID string   `json:"race_id"`
		LegacyFeePct  *float64 `json:"estimated_coinbase_deduction_pct"`
	}{observationAlias: (*observationAlias)(o)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if o.BlockID == "" {
		o.BlockID = aux.LegacyBlockID
	}
	if o.EstimatedPoolFeePct == nil {
		o.EstimatedPoolFeePct = aux.LegacyFeePct
	}
	return nil
}
