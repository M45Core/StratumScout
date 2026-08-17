package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObservationReadsVersion2Names(t *testing.T) {
	var observation Observation
	err := json.Unmarshal([]byte(`{"version":2,"race_id":"legacy-block","estimated_coinbase_deduction_pct":1.25}`), &observation)
	if err != nil {
		t.Fatal(err)
	}
	if observation.BlockID != "legacy-block" {
		t.Fatalf("block_id=%q", observation.BlockID)
	}
	if observation.EstimatedPoolFeePct == nil || *observation.EstimatedPoolFeePct != 1.25 {
		t.Fatalf("pool fee=%v", observation.EstimatedPoolFeePct)
	}
}

func TestObservationPrefersVersion3Names(t *testing.T) {
	var observation Observation
	err := json.Unmarshal([]byte(`{"version":3,"block_id":"current-block","estimated_pool_fee_pct":0.5,"race_id":"old"}`), &observation)
	if err != nil {
		t.Fatal(err)
	}
	if observation.BlockID != "current-block" {
		t.Fatalf("block_id=%q", observation.BlockID)
	}
	if observation.EstimatedPoolFeePct == nil || *observation.EstimatedPoolFeePct != 0.5 {
		t.Fatalf("pool fee=%v", observation.EstimatedPoolFeePct)
	}
}

func TestCurrentObservationVersionNeverSerializesWorkerDestination(t *testing.T) {
	const workerAddress = "12ZEw5Hcv1hTb6YUQJ69y1V7uhcoDz92PH"
	const workerScript = "76a914111111111111111111111111111111111111111188ac"
	const publicAddress = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	original := Observation{
		Version: ObservationVersion, CoinbaseAnalyzed: true, WorkerWalletInCoinbase: true,
		CoinbaseTotalSats: 100, WorkerPayoutSats: 99, CoinbaseOutputCount: 2,
		CoinbaseOutputs: []CoinbaseOutput{
			{ValueSats: 99, ScriptPubKey: workerScript, Address: workerAddress, ScriptType: "p2pkh", Worker: true},
			{ValueSats: 1, ScriptPubKey: "51", Address: publicAddress, ScriptType: "unknown"},
		},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{workerAddress, workerScript, "\"worker\":true"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("serialized observation exposed private worker destination %q: %s", private, encoded)
		}
	}
	if !strings.Contains(string(encoded), publicAddress) {
		t.Fatalf("serialized observation omitted public non-worker destination: %s", encoded)
	}
	var decoded Observation
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != ObservationVersion || decoded.CoinbaseOutputCount != 2 ||
		len(decoded.CoinbaseOutputs) != 1 || decoded.CoinbaseOutputs[0].Address != publicAddress || decoded.CoinbaseOutputs[0].Worker {
		t.Fatalf("decoded observation=%+v", decoded)
	}
}

func TestBlockSampleNeverSerializesWorkerDestination(t *testing.T) {
	const workerAddress = "12ZEw5Hcv1hTb6YUQJ69y1V7uhcoDz92PH"
	const workerScript = "76a914111111111111111111111111111111111111111188ac"
	const publicAddress = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	offset := 0.0
	original := Observation{
		Version: ObservationVersion, RecordType: RecordTypeBlockSample,
		EndpointSamples: []EndpointBlockSample{{
			PoolID: "pool", Endpoint: "pool.example:3333", OffsetMS: &offset,
			Coinbase: &CoinbaseEvidence{
				WorkerWalletInCoinbase: true, CoinbaseTotalSats: 100, WorkerPayoutSats: 99, CoinbaseOutputCount: 2,
				CoinbaseOutputs: []CoinbaseOutput{
					{ValueSats: 99, ScriptPubKey: workerScript, Address: workerAddress, ScriptType: "p2pkh", Worker: true},
					{ValueSats: 1, ScriptPubKey: "51", Address: publicAddress, ScriptType: "unknown"},
				},
			},
		}},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{workerAddress, workerScript, "\"worker\":true"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("serialized block sample exposed private worker destination %q: %s", private, encoded)
		}
	}
	if !strings.Contains(string(encoded), publicAddress) {
		t.Fatalf("serialized block sample omitted public non-worker destination: %s", encoded)
	}
}
