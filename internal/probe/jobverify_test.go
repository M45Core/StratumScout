package probe

import (
	"encoding/hex"
	"testing"
)

func TestVerifyJob(t *testing.T) {
	workerScript, _ := hex.DecodeString("76a914111111111111111111111111111111111111111188ac")
	coinbase1 := "0100000001" + zeroHex(32) + "ffffffff0c03a1bb0d"
	coinbase2 := "ffffffff01" + "00f2052a01000000" + "19" + hex.EncodeToString(workerScript) + "00000000"
	j := Job{PrevHash: zeroHex(32), Coinbase1: coinbase1, Coinbase2: coinbase2, MerkleBranches: []string{zeroHex(32)}, Version: "20000000", Bits: "17034219", NTime: "66ad0000", ExtraNonce1: "01020304", ExtraNonce2Size: 4, WorkerScript: workerScript}
	v := VerifyJob(j)
	if !v.Valid {
		t.Fatalf("valid job rejected: %v", v.Errors)
	}
	if len(v.MerkleRoot) != 64 {
		t.Fatalf("root=%q", v.MerkleRoot)
	}
	if v.BlockHeight != 900_000 {
		t.Fatalf("block height=%d, want 900000", v.BlockHeight)
	}
	if !v.CoinbaseAnalyzed || !v.WorkerWalletSeen || v.EstimatedPoolFeePct == nil || *v.EstimatedPoolFeePct != 0 {
		t.Fatalf("payout verification=%+v", v)
	}
	if v.CoinbaseOutputCount != 1 || len(v.CoinbaseOutputs) != 0 {
		t.Fatalf("retained payout output=%+v", v.CoinbaseOutputs)
	}
	j.Bits = "zz"
	if VerifyJob(j).Valid {
		t.Fatal("invalid bits accepted")
	}
}

func zeroHex(bytes int) string {
	out := make([]byte, bytes*2)
	for i := range out {
		out[i] = '0'
	}
	return string(out)
}
