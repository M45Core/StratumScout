package probe

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
)

type identityPreset struct {
	agents []string
	worker string
}

// Keep each Stratum user agent coupled to a plausible hardware worker name.
// These strings follow the mining.subscribe formats emitted by the respective
// firmware; several pools use them to select compatibility workarounds.
var identityPresets = []identityPreset{
	{agents: axeAgents("BM1397"), worker: "bitaxe-max"},
	{agents: axeAgents("BM1366"), worker: "bitaxe-ultra"},
	{agents: axeAgents("BM1366"), worker: "bitaxe-hex"},
	{agents: axeAgents("BM1368"), worker: "bitaxe-supra"},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma"},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma-duo"},
	{agents: axeAgents("BM1368"), worker: "bitaxe-supra-hex"},
	{agents: axeAgents("BM1370"), worker: "bitaxe-gamma-turbo"},
	{agents: nerdAgents("NerdAxe", "BM1366"), worker: "nerdaxe"},
	{agents: nerdAgents("NerdAxe", "BM1370"), worker: "nerdaxe-gamma"},
	{agents: nerdAgents("NerdQAxe+", "BM1368"), worker: "nerdqaxe-plus"},
	{agents: nerdAgents("NerdQAxe++", "BM1370"), worker: "nerdqaxe-plusplus"},
	{agents: nerdAgents("NerdOCTAXE-γ", "BM1370"), worker: "nerdoctaxe-gamma"},
	{agents: nerdAgents("NerdQX", "BM1370"), worker: "nerdqx"},
	{agents: nerdAgents("NerdEKO", "BM1370"), worker: "nerdeko"},
	{agents: []string{"NerdHaxe-γ/BM1370/v2.3.0"}, worker: "nerdhaxe-gamma"},
	{agents: []string{"Q1370/BM1370/v2.3.0"}, worker: "q1370"},
	{agents: []string{"Q1373/BM1373/v2.3.0"}, worker: "q1373"},
	{agents: []string{"cgminer/4.9.2", "cgminer/4.10.0", "cgminer/4.11.0", "cgminer/4.11.1"}, worker: "avalon-nano-3s"},
	{agents: []string{"bfgminer/5.4.2", "bfgminer/5.5.0"}, worker: "futurebit-apollo"},
	{agents: []string{"Antminer"}, worker: "antminer-s21"},
	{agents: []string{"bmminer/2.0"}, worker: "antminer-s19-pro"},
	{agents: []string{"bosminer/23.08", "bosminer/24.04", "bosminer/24.09", "bosminer/25.05", "bosminer/26.06"}, worker: "antminer-s19-braiins"},
	{agents: []string{"whatsminer/v1.0"}, worker: "whatsminer-m60s"},
	{agents: []string{"xminer-1.0"}, worker: "antminer-s21-vnish"},
	{agents: []string{"PowerPlay-BM/1.0"}, worker: "epic-blockminer"},
	{agents: []string{"NiceHash/1.0.0"}, worker: "nicehash-sha256"},
	{agents: []string{"LuckyMiner"}, worker: "luckyminer-lv07"},
	{agents: []string{"bitforge/BM1370/v1.0", "bitforge/BM1370/v1.5", "bitforge/BM1370/v1.6"}, worker: "bitforge-nano"},
	{agents: []string{"Zyber8S/v2.11.8-Zyber"}, worker: "zyber8s"},
	{agents: []string{"Zyber8G/v2.11.8-Zyber"}, worker: "zyber8g"},
	{agents: []string{"NMAxe/0.1.0", "NMAxe/v3.1.01", "NMAxe/v3.1.02"}, worker: "nmaxe"},
	{agents: []string{"bitdsk/E1"}, worker: "bitdsk-e1"},
	{agents: []string{"bitdsk/S1"}, worker: "bitdsk-s1"},
	{agents: []string{"mujina-miner/0.1.0-alpha"}, worker: "ember-one"},
	{agents: []string{"apollo"}, worker: "futurebit-apollo"},
}

func axeAgents(asic string) []string {
	return versionedAgents("bitaxe/"+asic+"/", []string{"v2.10.1", "v2.11.0", "v2.12.2", "v2.13.1", "v2.13.2", "v2.14.0", "v2.14.1", "v2.14.2"})
}

func nerdAgents(device, asic string) []string {
	return versionedAgents(device+"/"+asic+"/", []string{"1.0.37", "1.0.37.1", "1.0.37.2-LTS"})
}

func versionedAgents(prefix string, versions []string) []string {
	agents := make([]string, len(versions))
	for i, version := range versions {
		agents[i] = prefix + version
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

type Identity struct {
	Username     string
	Agent        string
	PayoutScript []byte
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
	preset, err := randomPreset()
	if err != nil {
		return Identity{}, err
	}
	worker, err := randomWorkerName(preset.worker)
	if err != nil {
		return Identity{}, err
	}
	payoutScript := append([]byte{0x76, 0xa9, 0x14}, payload[1:]...)
	payoutScript = append(payoutScript, 0x88, 0xac)
	username := address
	if worker != "" {
		username += "." + worker
	}
	agent, err := randomChoice(preset.agents)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Username: username, Agent: agent, PayoutScript: payoutScript}, nil
}

func randomPreset() (identityPreset, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(identityPresets))))
	if err != nil {
		return identityPreset{}, err
	}
	return identityPresets[n.Int64()], nil
}

func randomChoice(items []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		return "", err
	}
	return items[n.Int64()], nil
}

func randomWorkerName(device string) (string, error) {
	styleRoll, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return "", err
	}

	style := workerNameStyleForRoll(styleRoll.Int64())
	if style == workerNameBare {
		return "", nil
	}
	if style == workerNameNumeric {
		return fmt.Sprintf("%03d", n.Int64()), nil
	}

	root := deviceWorkerRoot(device)
	if style == workerNameGenericNumbered || style == workerNameGenericPlain {
		root, err = randomGenericWorkerRoot()
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

func workerNameStyleForRoll(roll int64) workerNameStyle {
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

func randomGenericWorkerRoot() (string, error) {
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
