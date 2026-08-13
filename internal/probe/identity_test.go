package probe

import (
	"regexp"
	"strings"
	"testing"
)

var workerNamePattern = regexp.MustCompile(`^[a-z0-9]{1,15}$`)

func TestRandomIdentity(t *testing.T) {
	a, err := RandomIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Username) < 30 || a.Agent == "" || len(a.PayoutScript) != 25 {
		t.Fatalf("bad identity: %+v", a)
	}
	b, err := RandomIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if a.Username == b.Username {
		t.Fatal("identities should rotate")
	}
}

func TestIdentityPresetsArePairedASICMiners(t *testing.T) {
	if len(identityPresets) < 35 {
		t.Fatalf("expected an expanded ASIC preset catalog, got %d entries", len(identityPresets))
	}

	agents := make(map[string]bool, len(identityPresets))
	for _, preset := range identityPresets {
		if len(preset.agents) == 0 || preset.worker == "" {
			t.Fatalf("empty identity preset: %+v", preset)
		}
		if strings.ContainsAny(preset.worker, ". ") {
			t.Fatalf("worker name cannot be embedded in wallet.<worker>: %q", preset.worker)
		}
		for _, agent := range preset.agents {
			if agent == "" {
				t.Fatalf("empty user agent in preset: %+v", preset)
			}
			for _, nonASIC := range []string{"cpuminer", "nerdminer", "termux", "leafminer", "tinyminer"} {
				if strings.Contains(strings.ToLower(agent), nonASIC) {
					t.Fatalf("excluded agent %q included in preset catalog", agent)
				}
			}
			agents[agent] = true
		}
	}

	for _, required := range []string{
		"bitaxe/BM1370/v2.14.2",
		"bfgminer/5.5.0",
		"whatsminer/v1.0",
		"bitforge/BM1370/v1.6",
		"Zyber8G/v2.11.8-Zyber",
		"NMAxe/v3.1.02",
		"bitdsk/S1",
		"mujina-miner/0.1.0-alpha",
	} {
		if !agents[required] {
			t.Errorf("missing source-verified mining agent %q", required)
		}
	}
}

func TestHardwareWorkerNamesMatchUserAgents(t *testing.T) {
	allowedAgentPrefixes := map[string][]string{
		"bitaxe-max":           {"bitaxe/BM1397/"},
		"bitaxe-ultra":         {"bitaxe/BM1366/"},
		"bitaxe-hex":           {"bitaxe/BM1366/"},
		"bitaxe-supra":         {"bitaxe/BM1368/"},
		"bitaxe-gamma":         {"bitaxe/BM1370/"},
		"bitaxe-gamma-duo":     {"bitaxe/BM1370/"},
		"bitaxe-supra-hex":     {"bitaxe/BM1368/"},
		"bitaxe-gamma-turbo":   {"bitaxe/BM1370/"},
		"nerdaxe":              {"NerdAxe/BM1366/"},
		"nerdaxe-gamma":        {"NerdAxe/BM1370/"},
		"nerdqaxe-plus":        {"NerdQAxe+/BM1368/"},
		"nerdqaxe-plusplus":    {"NerdQAxe++/BM1370/"},
		"nerdoctaxe-gamma":     {"NerdOCTAXE-γ/BM1370/"},
		"nerdqx":               {"NerdQX/BM1370/"},
		"nerdeko":              {"NerdEKO/BM1370/"},
		"nerdhaxe-gamma":       {"NerdHaxe-γ/BM1370/"},
		"q1370":                {"Q1370/BM1370/"},
		"q1373":                {"Q1373/BM1373/"},
		"avalon-nano-3s":       {"cgminer/"},
		"futurebit-apollo":     {"bfgminer/", "apollo"},
		"antminer-s21":         {"Antminer"},
		"antminer-s19-pro":     {"bmminer/"},
		"antminer-s19-braiins": {"bosminer/"},
		"whatsminer-m60s":      {"whatsminer/"},
		"antminer-s21-vnish":   {"xminer-"},
		"epic-blockminer":      {"PowerPlay-BM/"},
		"nicehash-sha256":      {"NiceHash/"},
		"luckyminer-lv07":      {"LuckyMiner"},
		"bitforge-nano":        {"bitforge/BM1370/"},
		"zyber8s":              {"Zyber8S/"},
		"zyber8g":              {"Zyber8G/"},
		"nmaxe":                {"NMAxe/"},
		"bitdsk-e1":            {"bitdsk/E1"},
		"bitdsk-s1":            {"bitdsk/S1"},
		"ember-one":            {"mujina-miner/"},
	}

	for _, preset := range identityPresets {
		allowed := allowedAgentPrefixes[preset.worker]
		if len(allowed) == 0 {
			t.Fatalf("hardware worker %q has no user-agent pairing rule", preset.worker)
		}
		for _, agent := range preset.agents {
			matched := false
			for _, prefix := range allowed {
				matched = matched || strings.HasPrefix(agent, prefix)
			}
			if !matched {
				t.Errorf("worker %q is incompatible with user-agent %q", preset.worker, agent)
			}
		}
	}
}

func TestVersionedFamiliesVaryWithoutChangingWireFormat(t *testing.T) {
	for prefix, agents := range map[string][]string{
		"bitaxe/BM1370/":     axeAgents("BM1370"),
		"NerdQAxe++/BM1370/": nerdAgents("NerdQAxe++", "BM1370"),
	} {
		if len(agents) < 3 {
			t.Fatalf("expected several released versions for %q, got %v", prefix, agents)
		}
		for _, agent := range agents {
			if !strings.HasPrefix(agent, prefix) {
				t.Errorf("agent %q does not preserve family wire format %q", agent, prefix)
			}
		}
	}
	if len(genericWorkerRoots) < 50 {
		t.Fatalf("generic worker root catalog is too narrow: %d", len(genericWorkerRoots))
	}
	allRoots := append([]string{}, genericWorkerRoots...)
	for _, parts := range []struct {
		prefixes []string
		suffixes []string
	}{
		{workerLocationPrefixes, workerRoleSuffixes},
		{workerNicknamePrefixes, workerNicknameNouns},
		{workerMiningPrefixes, workerMiningSuffixes},
	} {
		allRoots = append(allRoots, workerCompounds(parts.prefixes, parts.suffixes)...)
	}
	if len(allRoots) < 400 {
		t.Fatalf("composed worker vocabulary is too narrow: %d", len(allRoots))
	}
	for _, root := range allRoots {
		if !workerNamePattern.MatchString(root) {
			t.Errorf("generic worker root is not portable: %q", root)
		}
		if len(root) > 13 {
			t.Errorf("generic worker root leaves no room for a two-digit ordinal: %q", root)
		}
	}
}

func TestRandomWorkerNamesArePortable(t *testing.T) {
	for _, device := range []string{"bitaxe-gamma-turbo", "NerdOCTAXE-γ", "futurebit-apollo", "---"} {
		seen := make(map[string]bool)
		for range 100 {
			worker, err := randomWorkerName(device)
			if err != nil {
				t.Fatal(err)
			}
			if worker != "" && !workerNamePattern.MatchString(worker) {
				t.Fatalf("worker name is not portable: %q", worker)
			}
			seen[worker] = true
		}
		if len(seen) < 2 {
			t.Fatalf("worker names did not vary for %q: %v", device, seen)
		}
	}
}

func TestWorkerNameStyleWeights(t *testing.T) {
	counts := make(map[workerNameStyle]int)
	for roll := int64(0); roll < 20; roll++ {
		counts[workerNameStyleForRoll(roll)]++
	}
	want := map[workerNameStyle]int{
		workerNameBare:             5,
		workerNameHardwareNumbered: 5,
		workerNameHardwarePlain:    4,
		workerNameGenericNumbered:  3,
		workerNameGenericPlain:     2,
		workerNameNumeric:          1,
	}
	for style, wantCount := range want {
		if counts[style] != wantCount {
			t.Errorf("worker style %d has %d rolls, want %d", style, counts[style], wantCount)
		}
	}
}

// TestGeneratedIdentityExamples is intentionally verbose-only: go test hides
// these samples unless invoked with -v. It exercises the same paired preset and
// worker-name generators as RandomIdentity without creating or logging a wallet
// address or complete Stratum username.
func TestGeneratedIdentityExamples(t *testing.T) {
	const exampleCount = 48
	for i := range exampleCount {
		preset, err := randomPreset()
		if err != nil {
			t.Fatal(err)
		}
		agent, err := randomChoice(preset.agents)
		if err != nil {
			t.Fatal(err)
		}
		worker, err := randomWorkerName(preset.worker)
		if err != nil {
			t.Fatal(err)
		}
		if worker == "" {
			worker = "(no suffix)"
		} else if !workerNamePattern.MatchString(worker) {
			t.Fatalf("worker name is not portable: %q", worker)
		}
		t.Logf("%02d  user-agent=%-38q worker=%q", i+1, agent, worker)
	}
}

func TestDeviceWorkerRoot(t *testing.T) {
	for input, want := range map[string]string{
		"bitaxe-gamma-turbo": "bitaxegammat",
		"NerdOCTAXE-γ":       "nerdoctaxe",
		"---":                "worker",
	} {
		if got := deviceWorkerRoot(input); got != want {
			t.Errorf("deviceWorkerRoot(%q) = %q, want %q", input, got, want)
		}
	}
}
