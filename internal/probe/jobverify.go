package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/Distortions81/StratumScout/internal/model"
)

// Job contains the mining.notify fields needed to reconstruct the coinbase
// merkle root and sanity-check the candidate header. This is structural
// verification; Stratum V1 does not provide transaction bodies.
type Job struct {
	PrevHash, Coinbase1, Coinbase2 string
	MerkleBranches                 []string
	Version, Bits, NTime           string
	ExtraNonce1                    string
	ExtraNonce2Size                int
	WorkerScript                   []byte
}

type Verification struct {
	Valid                    bool
	Errors                   []string
	MerkleRoot               string
	CoinbaseAnalyzed         bool
	WorkerWalletSeen         bool
	CoinbaseTotalSats        uint64
	WorkerPayoutSats         uint64
	CoinbaseOutputs          []model.CoinbaseOutput
	CoinbaseOutputCount      int
	CoinbaseOutputsTruncated bool
	CoinbaseOmittedSats      uint64
	EstimatedPoolFeePct      *float64
}

func VerifyJob(j Job) Verification {
	var errs []string
	checkHex := func(name, value string, size int) []byte {
		raw, err := hex.DecodeString(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s is not hex", name))
			return nil
		}
		if size >= 0 && len(raw) != size {
			errs = append(errs, fmt.Sprintf("%s is %d bytes, want %d", name, len(raw), size))
		}
		return raw
	}
	checkHex("previous hash", j.PrevHash, 32)
	checkHex("version", j.Version, 4)
	bits := checkHex("bits", j.Bits, 4)
	checkHex("ntime", j.NTime, 4)
	cb1 := checkHex("coinbase1", j.Coinbase1, -1)
	cb2 := checkHex("coinbase2", j.Coinbase2, -1)
	ex1 := checkHex("extranonce1", j.ExtraNonce1, -1)
	if j.ExtraNonce2Size <= 0 || j.ExtraNonce2Size > 32 {
		errs = append(errs, "extranonce2 size is outside 1..32")
	}
	if len(bits) == 4 {
		compact := uint32(bits[0])<<24 | uint32(bits[1])<<16 | uint32(bits[2])<<8 | uint32(bits[3])
		exponent, mantissa := compact>>24, compact&0x007fffff
		if mantissa == 0 || compact&0x00800000 != 0 || exponent < 3 || exponent > 32 {
			errs = append(errs, "bits encodes an invalid proof-of-work target")
		} else {
			target := new(big.Int).SetUint64(uint64(mantissa))
			target.Lsh(target, uint(8*(exponent-3)))
			if target.Sign() <= 0 || target.BitLen() > 256 {
				errs = append(errs, "proof-of-work target is out of range")
			}
		}
	}
	branches := make([][]byte, 0, len(j.MerkleBranches))
	for i, branch := range j.MerkleBranches {
		raw := checkHex(fmt.Sprintf("merkle branch %d", i), branch, 32)
		if len(raw) == 32 {
			branches = append(branches, raw)
		}
	}
	if len(cb1)+len(cb2)+len(ex1)+j.ExtraNonce2Size < 10 {
		errs = append(errs, "reconstructed coinbase is implausibly short")
	}
	var root string
	var coinbaseAnalyzed, workerWalletSeen bool
	var coinbaseTotalSats, workerPayoutSats, coinbaseOmittedSats uint64
	var coinbaseOutputs []model.CoinbaseOutput
	var coinbaseOutputCount int
	var coinbaseOutputsTruncated bool
	var estimatedPoolFeePct *float64
	if len(errs) == 0 {
		coinbase := append(append(append(append([]byte{}, cb1...), ex1...), make([]byte, j.ExtraNonce2Size)...), cb2...)
		summary, err := analyzeCoinbase(coinbase, j.WorkerScript)
		if err != nil {
			errs = append(errs, fmt.Sprintf("coinbase transaction: %v", err))
		} else {
			coinbaseAnalyzed = true
			workerWalletSeen = summary.WorkerWalletSeen
			coinbaseTotalSats = summary.TotalSats
			workerPayoutSats = summary.WorkerSats
			coinbaseOutputs = summary.Outputs
			coinbaseOutputCount = summary.OutputCount
			coinbaseOutputsTruncated = summary.OutputsTruncated
			coinbaseOmittedSats = summary.OmittedSats
			if summary.WorkerWalletSeen && summary.TotalSats > 0 {
				fee := 100 * float64(summary.TotalSats-summary.WorkerSats) / float64(summary.TotalSats)
				estimatedPoolFeePct = &fee
			}
		}
		hash := doubleSHA256(coinbase)
		for _, branch := range branches {
			hash = doubleSHA256(append(append([]byte{}, hash...), branch...))
		}
		root = hex.EncodeToString(hash)
	}
	return Verification{Valid: len(errs) == 0, Errors: errs, MerkleRoot: root, CoinbaseAnalyzed: coinbaseAnalyzed, WorkerWalletSeen: workerWalletSeen, CoinbaseTotalSats: coinbaseTotalSats, WorkerPayoutSats: workerPayoutSats, CoinbaseOutputs: coinbaseOutputs, CoinbaseOutputCount: coinbaseOutputCount, CoinbaseOutputsTruncated: coinbaseOutputsTruncated, CoinbaseOmittedSats: coinbaseOmittedSats, EstimatedPoolFeePct: estimatedPoolFeePct}
}

func doubleSHA256(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:]
}
