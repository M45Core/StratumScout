package probe

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

type identityPreset struct {
	agents  []string
	worker  string
	weight  int
	profile workerProfile
}

type workerProfile uint8

const (
	workerProfileHome workerProfile = iota
	workerProfileIndustrial
	workerProfileRental
)

// Keep each Stratum user agent coupled to a plausible hardware worker name.
// These strings follow the mining.subscribe formats emitted by the respective
// firmware; several pools use them to select compatibility workarounds.
var identityPresets = []identityPreset{
	{agents: axeAgents("BM1397"), worker: "bitaxe-max", weight: 2},
	{agents: axeAgents("BM1366"), worker: "bitaxe-ultra", weight: 4},
	{agents: axeAgents("BM1366"), worker: "bitaxe-hex", weight: 4},
	{agents: axeAgents("BM1368"), worker: "bitaxe-supra", weight: 8},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma", weight: 40},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma-duo", weight: 10},
	{agents: axeAgents("BM1368"), worker: "bitaxe-supra-hex", weight: 8},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma-turbo", weight: 24},
	{agents: nerdAgents("NerdAxe", "BM1366"), worker: "nerdaxe", weight: 3},
	{agents: nerdAgents("NerdAxe", "BM1370"), worker: "nerdaxe-gamma", weight: 5},
	{agents: nerdAgents("NerdQAxe+", "BM1368"), worker: "nerdqaxe-plus", weight: 5},
	{agents: nerdAgents("NerdQAxe++", "BM1370"), worker: "nerdqaxe-plusplus", weight: 20},
	{agents: nerdAgents("NerdOCTAXE-γ", "BM1370"), worker: "nerdoctaxe-gamma", weight: 10},
	{agents: nerdAgents("NerdQX", "BM1370"), worker: "nerdqx", weight: 3},
	{agents: nerdAgents("NerdEKO", "BM1370"), worker: "nerdeko", weight: 2},
	{agents: []string{"NerdHaxe-γ/BM1370/v2.3.0"}, worker: "nerdhaxe-gamma", weight: 1},
	{agents: []string{"Q1370/BM1370/v2.3.0"}, worker: "q1370", weight: 1},
	{agents: []string{"Q1373/BM1373/v2.3.0"}, worker: "q1373", weight: 2},
	{agents: weightedVersionedAgents("cgminer/", []versionWeight{{"4.9.2", 1}, {"4.10.0", 1}, {"4.11.0", 1}, {"4.11.1", 17}}), worker: "avalon-nano-3s", weight: 22},
	{agents: weightedVersionedAgents("bfgminer/", []versionWeight{{"5.4.2", 1}, {"5.5.0", 4}}), worker: "futurebit-apollo", weight: 1},
	{agents: []string{"Antminer", "Antminer S21/Thu Oct  9 17:56:18 CST 2025"}, worker: "antminer-s21", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{"bmminer/2.0", "bmminer/4.11.1 rwglr"}, worker: "antminer-s19-pro", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{
		"2022-09-27-0-26ba61b9-22.08.1-plus;bosminer-plus-am1-s9 0.9.0-26ba61b9",
		"2024-07-12-0-fc9fe388-24.06-plus;bosminer-plus-tuner 0.9.0-fc9fe388",
		"2026-05-27-0-db3ed535-26.06-plus;bosminer-plus-tuner 0.9.0-db3ed535",
		"2026-07-07-0-c5a2978a-26.07-plus;bosminer-plus-tuner 0.9.0-c5a2978a",
	}, worker: "antminer-s19-braiins", weight: 2, profile: workerProfileIndustrial},
	{agents: []string{"whatsminer/v1.0"}, worker: "whatsminer-m60s", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{"xminer-1.0", "xminer-1.2.7", "xminer-1.2.7"}, worker: "antminer-s21-vnish", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{"PowerPlay-BM/1.0", "PowerPlay-BMS/1.24.0", "PowerPlay-BMS/1.24.0"}, worker: "epic-blockminer", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{"NiceHash/1.0.0"}, worker: "nicehash-sha256", weight: 1, profile: workerProfileRental},
	{agents: []string{"LuckyMiner", "LuckyMiner/BM1366/1.0.0", "LuckyMiner/BM1366/1.1.0", "LuckyMiner/BM1366/1.2.0", "LuckyMiner/BM1366/1.2.0"}, worker: "luckyminer-lv07", weight: 2},
	{agents: weightedVersionedAgents("bitforge/BM1370/", []versionWeight{{"v1.0", 3}, {"v1.5", 2}, {"v1.6", 5}}), worker: "bitforge-nano", weight: 3},
	{agents: []string{"Zyber8S/v2.11.8-Zyber"}, worker: "zyber8s", weight: 1},
	{agents: []string{"Zyber8G/v2.11.8-Zyber"}, worker: "zyber8g", weight: 1},
	{agents: weightedVersionedAgents("NMAxe/", []versionWeight{{"v2.8.02", 1}, {"v3.0.10", 1}, {"v3.0.20", 1}, {"v3.0.21", 2}, {"v3.1.01", 3}, {"v3.1.02", 4}}), worker: "nmaxe", weight: 2},
	{agents: []string{"bitdsk/E1"}, worker: "bitdsk-e1", weight: 1},
	{agents: []string{"bitdsk/S1"}, worker: "bitdsk-s1", weight: 1},
	{agents: []string{"mujina-miner/0.1.0-alpha"}, worker: "ember-one", weight: 1, profile: workerProfileIndustrial},
	{agents: []string{"apollo", "apollo-miner 2.0.3 2025-03-19, msp ver 0xd167"}, worker: "futurebit-apollo", weight: 1},
}

func axeAgents(asic string) []string {
	return weightedVersionedAgents("bitaxe/"+asic+"/", []versionWeight{
		{"v2.10.1", 1}, {"v2.11.0", 1}, {"v2.12.2", 2}, {"v2.13.1", 4},
		{"v2.13.2", 1}, {"v2.14.0", 2}, {"v2.14.1", 3}, {"v2.14.2", 6},
	})
}

func nerdAgents(device, asic string) []string {
	return weightedVersionedAgents(device+"/"+asic+"/", []versionWeight{
		{"v1.0.35", 1}, {"v1.0.36", 3}, {"v1.0.37.1", 3}, {"V1.0.37.2-LTS", 7},
	})
}

type versionWeight struct {
	version string
	weight  int
}

func weightedVersionedAgents(prefix string, versions []versionWeight) []string {
	count := 0
	for _, version := range versions {
		count += version.weight
	}
	agents := make([]string, 0, count)
	for _, version := range versions {
		for range version.weight {
			agents = append(agents, prefix+version.version)
		}
	}
	return agents
}

// Public worker listings are dominated by hardware names (handled by each
// preset above). The remaining names mostly resemble role-plus-number labels,
// location/role compounds, mining nicknames, or operational labels. Keep the
// roots generic rather than copying identifiable names from a pool. Generated
// names stay lowercase alphanumeric and at most 15 characters.
var genericWorkerRoots = []string{
	// Generic role labels, commonly followed by an ordinal.
	"worker", "miner", "rig", "asic", "node", "unit", "box", "device",
	// Placement labels.
	"home", "garage", "basement", "shed", "shop", "office", "rack", "desk",
	"attic", "loft", "workshop", "backyard", "upstairs", "downstairs",
	// Mining-themed and nickname-shaped labels.
	"solo", "lucky", "lottery", "hash", "block", "bitcoin", "satoshi", "pleb",
	"solominer", "blockhunter", "hashhunter", "lonewolf",
	// Fleet-management labels.
	"main", "primary", "backup", "test", "rental", "local", "remote", "spare",
	"first", "second", "alpha", "beta", "daily", "night", "weekend", "default",
}

var workerLocationPrefixes = []string{
	"home", "garage", "basement", "shed", "shop", "office", "rack", "desk",
	"attic", "loft", "workshop", "backyard", "upstairs", "downstairs",
}

var workerRoleSuffixes = []string{"rig", "miner", "node", "asic", "box", "unit"}

var workerNicknamePrefixes = []string{
	"lucky", "lone", "quiet", "rapid", "steady", "little", "wild", "happy",
	"sleepy", "red", "blue", "green", "golden", "silver", "old", "new",
}

var workerNicknameNouns = []string{
	"wolf", "fox", "bear", "bee", "cat", "dog", "owl", "raven", "badger",
	"rabbit", "dragon", "phoenix", "gecko", "moose", "hawk",
}

// Do not compose brand-like names here. For example, LuckyMiner is a real
// miner family and is emitted only by its matching identity preset.
var workerMiningPrefixes = []string{"hash", "block", "solo", "coin", "btc", "bitcoin", "satoshi"}
var workerMiningSuffixes = []string{"rig", "miner", "node", "box", "lab", "farm", "works", "forge", "hunter"}

var industrialWorkerRoots = []string{
	"worker", "miner", "asic", "unit", "machine", "rack", "row", "shelf",
	"site", "farm", "hall", "pod", "zone", "line", "primary", "backup",
}

var industrialLocationPrefixes = []string{"rack", "row", "shelf", "site", "farm", "hall", "pod", "zone", "line"}
var industrialRoleSuffixes = []string{"miner", "asic", "unit", "node", "rig"}

var rentalWorkerRoots = []string{
	"rental", "order", "job", "contract", "hashpower", "buyer", "market",
	"primary", "backup", "worker", "rig",
}

type Identity struct {
	Username           string
	Agent              string
	WorkerScriptSHA256 string
	wireStyle          stratumWireStyle
}

func RandomIdentity() (Identity, error) {
	payload := make([]byte, 21)
	payload[0] = 0
	if _, err := rand.Read(payload[1:]); err != nil {
		return Identity{}, err
	}
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	address := base58(append(payload, second[:4]...))
	payoutScript := make([]byte, 0, 25)
	payoutScript = append(payoutScript, 0x76, 0xa9, 0x14)
	payoutScript = append(payoutScript, payload[1:]...)
	payoutScript = append(payoutScript, 0x88, 0xac)
	payoutScriptHash := sha256.Sum256(payoutScript)
	preset, err := randomPreset()
	if err != nil {
		return Identity{}, err
	}
	worker, err := randomWorkerName(preset.worker, preset.profile)
	if err != nil {
		return Identity{}, err
	}
	username := address
	if worker != "" {
		username += "." + worker
	}
	agent, err := randomChoice(preset.agents)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Username: username, Agent: agent,
		WorkerScriptSHA256: hex.EncodeToString(payoutScriptHash[:]),
		wireStyle:          stratumWireStyleForAgent(agent),
	}, nil
}

type stratumWireStyle uint8

const (
	stratumWireCompact stratumWireStyle = iota
	stratumWireSpaced
)

func stratumWireStyleForAgent(agent string) stratumWireStyle {
	for _, prefix := range []string{"Nerd", "Q137", "NMAxe", "NMQAxe"} {
		if strings.HasPrefix(agent, prefix) {
			return stratumWireSpaced
		}
	}
	return stratumWireCompact
}

func randomPreset() (identityPreset, error) {
	totalWeight := 0
	for _, preset := range identityPresets {
		totalWeight += preset.weight
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(totalWeight)))
	if err != nil {
		return identityPreset{}, err
	}
	roll := int(n.Int64())
	for _, preset := range identityPresets {
		if roll < preset.weight {
			return preset, nil
		}
		roll -= preset.weight
	}
	return identityPreset{}, fmt.Errorf("identity preset weights are invalid")
}

func randomChoice(items []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		return "", err
	}
	return items[n.Int64()], nil
}

func randomWorkerName(device string, profile workerProfile) (string, error) {
	styleRoll, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return "", err
	}

	style := workerNameStyleForRoll(profile, styleRoll.Int64())
	if style == workerNameBare {
		return "", nil
	}
	if style == workerNameNumeric {
		return fmt.Sprintf("%03d", n.Int64()), nil
	}

	root := deviceWorkerRoot(device)
	if style == workerNameGenericNumbered || style == workerNameGenericPlain {
		root, err = randomGenericWorkerRoot(profile)
		if err != nil {
			return "", err
		}
	}
	if style == workerNameHardwarePlain || style == workerNameGenericPlain {
		return root, nil
	}
	return fmt.Sprintf("%s%02d", root, n.Int64()), nil
}

type workerNameStyle uint8

const (
	workerNameBare workerNameStyle = iota
	workerNameHardwareNumbered
	workerNameHardwarePlain
	workerNameGenericNumbered
	workerNameGenericPlain
	workerNameNumeric
)

func workerNameStyleForRoll(profile workerProfile, roll int64) workerNameStyle {
	if profile == workerProfileIndustrial {
		switch {
		case roll < 1: // 5%
			return workerNameBare
		case roll < 5: // 20%
			return workerNameHardwareNumbered
		case roll < 7: // 10%
			return workerNameHardwarePlain
		case roll < 14: // 35%
			return workerNameGenericNumbered
		case roll < 16: // 10%
			return workerNameGenericPlain
		default: // 20%
			return workerNameNumeric
		}
	}
	if profile == workerProfileRental {
		switch {
		case roll < 1: // 5%
			return workerNameBare
		case roll < 2: // 5%
			return workerNameHardwareNumbered
		case roll < 3: // 5%
			return workerNameHardwarePlain
		case roll < 8: // 25%
			return workerNameGenericNumbered
		case roll < 16: // 40%
			return workerNameGenericPlain
		default: // 20%
			return workerNameNumeric
		}
	}
	switch {
	case roll < 5: // 25%
		return workerNameBare
	case roll < 10: // 25%
		return workerNameHardwareNumbered
	case roll < 14: // 20%
		return workerNameHardwarePlain
	case roll < 17: // 15%
		return workerNameGenericNumbered
	case roll < 19: // 10%
		return workerNameGenericPlain
	default: // 5%
		return workerNameNumeric
	}
}

func randomGenericWorkerRoot(profile workerProfile) (string, error) {
	if profile == workerProfileIndustrial {
		style, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		if style.Int64() < 6 {
			return randomChoice(industrialWorkerRoots)
		}
		return randomCompound(industrialLocationPrefixes, industrialRoleSuffixes)
	}
	if profile == workerProfileRental {
		return randomChoice(rentalWorkerRoots)
	}
	style, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return "", err
	}
	switch {
	case style.Int64() < 8:
		return randomChoice(genericWorkerRoots)
	case style.Int64() < 13:
		return randomCompound(workerLocationPrefixes, workerRoleSuffixes)
	case style.Int64() < 17:
		return randomCompound(workerNicknamePrefixes, workerNicknameNouns)
	default:
		return randomCompound(workerMiningPrefixes, workerMiningSuffixes)
	}
}

func randomCompound(prefixes, suffixes []string) (string, error) {
	return randomChoice(workerCompounds(prefixes, suffixes))
}

func workerCompounds(prefixes, suffixes []string) []string {
	compounds := make([]string, 0, len(prefixes)*len(suffixes))
	for _, prefix := range prefixes {
		for _, suffix := range suffixes {
			root := prefix + suffix
			if len(root) <= 13 {
				compounds = append(compounds, root)
			}
		}
	}
	return compounds
}

func deviceWorkerRoot(device string) string {
	var root strings.Builder
	for _, c := range strings.ToLower(device) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			root.WriteRune(c)
			if root.Len() == 12 {
				break
			}
		}
	}
	if root.Len() == 0 {
		return "worker"
	}
	return root.String()
}

func base58(input []byte) string {
	alphabet := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)
	out := make([]byte, 0, 35)
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
