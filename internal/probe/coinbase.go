package probe

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/Distortions81/StratumScout/internal/model"
)

const (
	maxCoinbaseOutputsStored = model.MaxRetainedCoinbaseOutputs
	maxRetainedScriptBytes   = model.MaxRetainedCoinbaseScriptBytes
	bech32Charset            = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
)

type coinbaseSummary struct {
	TotalSats        uint64
	WorkerSats       uint64
	WorkerWalletSeen bool
	Outputs          []model.CoinbaseOutput
	OutputCount      int
	OutputsTruncated bool
	OmittedSats      uint64
}

// analyzeCoinbase parses both legacy and witness transaction serialization.
// Matching uses the exact scriptPubKey generated for this probe session.
func analyzeCoinbase(raw, workerScript []byte) (coinbaseSummary, error) {
	var result coinbaseSummary
	destinations := make(map[string]*model.CoinbaseOutput)
	cursor := 0
	take := func(n int) ([]byte, error) {
		if n < 0 || cursor > len(raw)-n {
			return nil, fmt.Errorf("coinbase truncated at byte %d", cursor)
		}
		value := raw[cursor : cursor+n]
		cursor += n
		return value, nil
	}
	if _, err := take(4); err != nil {
		return result, err
	}
	witness := false
	if len(raw)-cursor >= 2 && raw[cursor] == 0x00 && raw[cursor+1] != 0x00 {
		witness = true
		cursor += 2
	}
	inputs, err := readCompactSize(raw, &cursor)
	if err != nil {
		return result, fmt.Errorf("input count: %w", err)
	}
	if inputs == 0 || inputs > 1000 {
		return result, fmt.Errorf("implausible input count %d", inputs)
	}
	for i := uint64(0); i < inputs; i++ {
		if _, err := take(36); err != nil {
			return result, err
		}
		scriptLen, err := readCompactSize(raw, &cursor)
		if err != nil {
			return result, err
		}
		if scriptLen > uint64(len(raw)) {
			return result, fmt.Errorf("input script too large")
		}
		if _, err := take(int(scriptLen) + 4); err != nil {
			return result, err
		}
	}
	outputs, err := readCompactSize(raw, &cursor)
	if err != nil {
		return result, fmt.Errorf("output count: %w", err)
	}
	if outputs == 0 || outputs > 10000 {
		return result, fmt.Errorf("implausible output count %d", outputs)
	}
	result.OutputCount = int(outputs)
	for i := uint64(0); i < outputs; i++ {
		valueBytes, err := take(8)
		if err != nil {
			return result, err
		}
		value := binary.LittleEndian.Uint64(valueBytes)
		if ^uint64(0)-result.TotalSats < value {
			return result, fmt.Errorf("output value overflow")
		}
		result.TotalSats += value
		scriptLen, err := readCompactSize(raw, &cursor)
		if err != nil {
			return result, err
		}
		if scriptLen > uint64(len(raw)) {
			return result, fmt.Errorf("output script too large")
		}
		script, err := take(int(scriptLen))
		if err != nil {
			return result, err
		}
		worker := value > 0 && len(workerScript) > 0 && bytes.Equal(script, workerScript)
		if worker {
			result.WorkerWalletSeen = true
			if ^uint64(0)-result.WorkerSats < value {
				return result, fmt.Errorf("worker output overflow")
			}
			result.WorkerSats += value
		}
		// A matched payout is used only for aggregate verification and fee
		// calculation. Never retain its script or derived address in telemetry.
		if value == 0 || worker {
			continue
		}
		key := string(script)
		if output := destinations[key]; output != nil {
			if ^uint64(0)-output.ValueSats < value {
				return result, fmt.Errorf("destination output overflow")
			}
			output.ValueSats += value
			continue
		}
		address, scriptType := describeOutputScript(script)
		scriptHex, scriptTruncated := retainedScriptHex(script)
		destinations[key] = &model.CoinbaseOutput{
			ValueSats:             value,
			ScriptPubKey:          scriptHex,
			ScriptPubKeyTruncated: scriptTruncated,
			Address:               address,
			ScriptType:            scriptType,
		}
	}
	if witness {
		for i := uint64(0); i < inputs; i++ {
			items, err := readCompactSize(raw, &cursor)
			if err != nil {
				return result, err
			}
			if items > 1000 {
				return result, fmt.Errorf("implausible witness item count")
			}
			for j := uint64(0); j < items; j++ {
				itemLen, err := readCompactSize(raw, &cursor)
				if err != nil {
					return result, err
				}
				if itemLen > uint64(len(raw)) {
					return result, fmt.Errorf("witness item too large")
				}
				if _, err := take(int(itemLen)); err != nil {
					return result, err
				}
			}
		}
	}
	if _, err := take(4); err != nil {
		return result, fmt.Errorf("locktime: %w", err)
	}
	if cursor != len(raw) {
		return result, fmt.Errorf("coinbase has %d trailing bytes", len(raw)-cursor)
	}
	result.Outputs = make([]model.CoinbaseOutput, 0, len(destinations))
	for _, output := range destinations {
		result.Outputs = append(result.Outputs, *output)
	}
	sort.SliceStable(result.Outputs, func(i, j int) bool {
		left, right := result.Outputs[i], result.Outputs[j]
		if left.ValueSats != right.ValueSats {
			return left.ValueSats > right.ValueSats
		}
		return left.ScriptPubKey < right.ScriptPubKey
	})
	if len(result.Outputs) > maxCoinbaseOutputsStored {
		for _, output := range result.Outputs[maxCoinbaseOutputsStored:] {
			result.OmittedSats += output.ValueSats
		}
		result.Outputs = result.Outputs[:maxCoinbaseOutputsStored]
		result.OutputsTruncated = true
	}
	return result, nil
}

func retainedScriptHex(script []byte) (string, bool) {
	if len(script) <= maxRetainedScriptBytes {
		return hex.EncodeToString(script), false
	}
	return hex.EncodeToString(script[:maxRetainedScriptBytes]), true
}

func describeOutputScript(script []byte) (string, string) {
	if len(script) == 25 && bytes.Equal(script[:3], []byte{0x76, 0xa9, 0x14}) && bytes.Equal(script[23:], []byte{0x88, 0xac}) {
		return base58Check(0x00, script[3:23]), "p2pkh"
	}
	if len(script) == 23 && bytes.Equal(script[:2], []byte{0xa9, 0x14}) && script[22] == 0x87 {
		return base58Check(0x05, script[2:22]), "p2sh"
	}
	if len(script) >= 4 {
		version := -1
		switch {
		case script[0] == 0x00:
			version = 0
		case script[0] >= 0x51 && script[0] <= 0x60:
			version = int(script[0] - 0x50)
		}
		programLen := int(script[1])
		if version >= 0 && programLen == len(script)-2 && programLen >= 2 && programLen <= 40 && (version != 0 || programLen == 20 || programLen == 32) {
			scriptType := fmt.Sprintf("witness_v%d", version)
			switch {
			case version == 0 && programLen == 20:
				scriptType = "p2wpkh"
			case version == 0 && programLen == 32:
				scriptType = "p2wsh"
			case version == 1 && programLen == 32:
				scriptType = "p2tr"
			}
			return encodeWitnessAddress(version, script[2:]), scriptType
		}
	}
	if (len(script) == 35 && script[0] == 0x21 && script[34] == 0xac) || (len(script) == 67 && script[0] == 0x41 && script[66] == 0xac) {
		return "", "p2pk"
	}
	if len(script) > 0 && script[0] == 0x6a {
		return "", "op_return"
	}
	return "", "unknown"
}

func base58Check(prefix byte, payload []byte) string {
	value := append([]byte{prefix}, payload...)
	checksum := doubleSHA256(value)
	value = append(value, checksum[:4]...)
	return base58(value)
}

func encodeWitnessAddress(version int, program []byte) string {
	data := []byte{byte(version)}
	data = append(data, bytesToBase32(program)...)
	checksumConstant := uint32(1)
	if version > 0 {
		checksumConstant = 0x2bc830a3
	}
	values := append(bech32HRPExpand("bc"), data...)
	values = append(values, make([]byte, 6)...)
	polymod := bech32Polymod(values) ^ checksumConstant
	checksum := make([]byte, 6)
	for i := range checksum {
		checksum[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	encoded := append([]byte("bc"), 0x31)
	for _, value := range append(data, checksum...) {
		encoded = append(encoded, bech32Charset[value])
	}
	return string(encoded)
}

func bytesToBase32(input []byte) []byte {
	output := make([]byte, 0, (len(input)*8+4)/5)
	var accumulator uint32
	var bits uint
	for _, value := range input {
		accumulator = (accumulator << 8) | uint32(value)
		bits += 8
		for bits >= 5 {
			bits -= 5
			output = append(output, byte((accumulator>>bits)&31))
		}
	}
	if bits > 0 {
		output = append(output, byte((accumulator<<(5-bits))&31))
	}
	return output
}

func bech32HRPExpand(hrp string) []byte {
	output := make([]byte, 0, len(hrp)*2+1)
	for i := range hrp {
		output = append(output, hrp[i]>>5)
	}
	output = append(output, 0)
	for i := range hrp {
		output = append(output, hrp[i]&31)
	}
	return output
}

func bech32Polymod(values []byte) uint32 {
	generators := [...]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	checksum := uint32(1)
	for _, value := range values {
		top := checksum >> 25
		checksum = (checksum&0x1ffffff)<<5 ^ uint32(value)
		for i, generator := range generators {
			if (top>>uint(i))&1 != 0 {
				checksum ^= generator
			}
		}
	}
	return checksum
}

func readCompactSize(raw []byte, cursor *int) (uint64, error) {
	if *cursor >= len(raw) {
		return 0, fmt.Errorf("missing compact size")
	}
	prefix := raw[*cursor]
	*cursor++
	switch prefix {
	case 0xfd:
		if *cursor > len(raw)-2 {
			return 0, fmt.Errorf("truncated uint16")
		}
		value := uint64(binary.LittleEndian.Uint16(raw[*cursor : *cursor+2]))
		*cursor += 2
		if value < 0xfd {
			return 0, fmt.Errorf("non-canonical compact size")
		}
		return value, nil
	case 0xfe:
		if *cursor > len(raw)-4 {
			return 0, fmt.Errorf("truncated uint32")
		}
		value := uint64(binary.LittleEndian.Uint32(raw[*cursor : *cursor+4]))
		*cursor += 4
		if value <= 0xffff {
			return 0, fmt.Errorf("non-canonical compact size")
		}
		return value, nil
	case 0xff:
		if *cursor > len(raw)-8 {
			return 0, fmt.Errorf("truncated uint64")
		}
		value := binary.LittleEndian.Uint64(raw[*cursor : *cursor+8])
		*cursor += 8
		if value <= 0xffffffff {
			return 0, fmt.Errorf("non-canonical compact size")
		}
		return value, nil
	default:
		return uint64(prefix), nil
	}
}
