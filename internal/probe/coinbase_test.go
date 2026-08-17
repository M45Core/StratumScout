package probe

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestAnalyzeCoinbaseFindsWorkerOutputs(t *testing.T) {
	workerScript, _ := hex.DecodeString("76a914111111111111111111111111111111111111111188ac")
	raw, _ := hex.DecodeString("0100000001" + zeroHex(32) + "ffffffff020101ffffffff01" + "00f2052a01000000" + "19" + hex.EncodeToString(workerScript) + "00000000")
	summary, err := analyzeCoinbase(raw, workerScript)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.WorkerWalletSeen || summary.WorkerSats != 5_000_000_000 || summary.TotalSats != 5_000_000_000 || len(summary.Outputs) != 0 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestAnalyzeWitnessCoinbase(t *testing.T) {
	workerScript, _ := hex.DecodeString("76a914222222222222222222222222222222222222222288ac")
	raw, _ := hex.DecodeString("02000000000101" + zeroHex(32) + "ffffffff020101ffffffff01" + "0100000000000000" + "19" + hex.EncodeToString(workerScript) + "0120" + zeroHex(32) + "00000000")
	summary, err := analyzeCoinbase(raw, workerScript)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.WorkerWalletSeen || summary.TotalSats != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestAnalyzeCoinbaseRejectsTrailingData(t *testing.T) {
	raw, _ := hex.DecodeString("0100000001" + zeroHex(32) + "ffffffff020101ffffffff0101000000000000000000000000ff")
	if _, err := analyzeCoinbase(raw, nil); err == nil {
		t.Fatal("accepted trailing data")
	}
}

func TestAnalyzeCoinbaseRejectsNonCoinbaseTransactions(t *testing.T) {
	validInput := "01" + zeroHex(32) + "ffffffff020101ffffffff"
	validOutput := "0101000000000000000000000000"
	for name, transaction := range map[string]string{
		"multiple inputs":        "0100000002",
		"non-null previous hash": "0100000001" + "01" + zeroHex(31) + "ffffffff020101ffffffff" + validOutput,
		"short coinbase script":  "0100000001" + zeroHex(32) + "ffffffff0101ffffffff" + validOutput,
		"unknown witness flag":   "010000000002" + validInput + validOutput,
		"excessive output value": "01000000" + validInput + "01" + "010040075af07500" + "00" + "00000000",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := hex.DecodeString(transaction)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := analyzeCoinbase(raw, nil); err == nil {
				t.Fatal("malformed coinbase transaction was accepted")
			}
		})
	}
}

func TestAnalyzeCoinbaseAllowsUnclaimedReward(t *testing.T) {
	raw, err := hex.DecodeString("01000000" + "01" + zeroHex(32) + "ffffffff020101ffffffff" + "01" + zeroHex(8) + "00" + "00000000")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := analyzeCoinbase(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalSats != 0 || summary.OutputCount != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestDescribeOutputScriptUsesBech32AndBech32m(t *testing.T) {
	tests := []struct {
		script, address, scriptType string
	}{
		{
			script:     "0014751e76e8199196d454941c45d1b3a323f1433bd6",
			address:    "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
			scriptType: "p2wpkh",
		},
		{
			script:     "512079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
			address:    "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0",
			scriptType: "p2tr",
		},
	}
	for _, test := range tests {
		script, err := hex.DecodeString(test.script)
		if err != nil {
			t.Fatal(err)
		}
		address, scriptType := describeOutputScript(script)
		if address != test.address || scriptType != test.scriptType {
			t.Errorf("describeOutputScript(%s) = %q, %q; want %q, %q", test.script, address, scriptType, test.address, test.scriptType)
		}
	}
}

func TestAnalyzeCoinbaseBoundsRetainedDestinations(t *testing.T) {
	var raw bytes.Buffer
	if err := binary.Write(&raw, binary.LittleEndian, uint32(1)); err != nil {
		t.Fatal(err)
	}
	raw.WriteByte(1)
	raw.Write(make([]byte, 32))
	if err := binary.Write(&raw, binary.LittleEndian, uint32(0xffffffff)); err != nil {
		t.Fatal(err)
	}
	raw.WriteByte(2)
	raw.Write([]byte{1, 1})
	if err := binary.Write(&raw, binary.LittleEndian, uint32(0xffffffff)); err != nil {
		t.Fatal(err)
	}
	raw.WriteByte(maxCoinbaseOutputsStored + 1)
	for i := 0; i <= maxCoinbaseOutputsStored; i++ {
		if err := binary.Write(&raw, binary.LittleEndian, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
		raw.WriteByte(1)
		raw.WriteByte(byte(i + 1))
	}
	if err := binary.Write(&raw, binary.LittleEndian, uint32(0)); err != nil {
		t.Fatal(err)
	}

	summary, err := analyzeCoinbase(raw.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OutputCount != maxCoinbaseOutputsStored+1 || len(summary.Outputs) != maxCoinbaseOutputsStored || !summary.OutputsTruncated || summary.OmittedSats != 1 {
		t.Fatalf("bounded summary=%+v", summary)
	}
	if summary.Outputs[0].ValueSats != maxCoinbaseOutputsStored+1 {
		t.Fatalf("largest retained output=%d", summary.Outputs[0].ValueSats)
	}
}

func TestAnalyzeCoinbaseDoesNotTreatZeroValueWorkerScriptAsPayout(t *testing.T) {
	workerScript, err := hex.DecodeString("76a914333333333333333333333333333333333333333388ac")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString("0100000001" + zeroHex(32) + "ffffffff020101ffffffff02" + "0000000000000000" + "19" + hex.EncodeToString(workerScript) + "0100000000000000" + "01" + "51" + "00000000")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := analyzeCoinbase(raw, workerScript)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WorkerWalletSeen || summary.WorkerSats != 0 || summary.TotalSats != 1 || len(summary.Outputs) != 1 {
		t.Fatalf("zero-value worker script counted as payout: %+v", summary)
	}
}

func TestDecodeCoinbaseHeightRequiresMinimalScriptNumber(t *testing.T) {
	if _, ok := decodeCoinbaseHeight([]byte{2, 1, 0}); ok {
		t.Fatal("non-minimal block height was accepted")
	}
	if height, ok := decodeCoinbaseHeight([]byte{2, 0x80, 0}); !ok || height != 128 {
		t.Fatalf("minimal positive block height decoded as %d, %t", height, ok)
	}
}
