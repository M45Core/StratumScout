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
		if len(preset.agents) == 0 || preset.worker == "" || preset.weight <= 0 {
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
		"antminer-s19-braiins": {"20"},
		"whatsminer-m60s":      {"whatsminer/"},
		"antminer-s21-vnish":   {"xminer-"},
		"epic-blockminer":      {"PowerPlay-BM"},
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

func TestIdentityPresetWeightsFollowObservedFamilyMix(t *testing.T) {
	var total, bitaxe, nerd, cgminer int
	for _, preset := range identityPresets {
		total += preset.weight
		agent := preset.agents[0]
		switch {
		case strings.HasPrefix(agent, "bitaxe/"):
			bitaxe += preset.weight
		case strings.HasPrefix(agent, "Nerd"), strings.HasPrefix(agent, "Q137"):
			nerd += preset.weight
		case strings.HasPrefix(agent, "cgminer/"):
			cgminer += preset.weight
		}
	}
	if total != 196 || bitaxe != 100 || nerd != 52 || cgminer != 22 {
		t.Fatalf("unexpected preset weights: total=%d bitaxe=%d nerd=%d cgminer=%d", total, bitaxe, nerd, cgminer)
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
	allRoots = append(allRoots, industrialWorkerRoots...)
	allRoots = append(allRoots, workerCompounds(industrialLocationPrefixes, industrialRoleSuffixes)...)
	allRoots = append(allRoots, rentalWorkerRoots...)
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

func TestCurrentFirmwareVersionsAreMoreLikely(t *testing.T) {
	for _, test := range []struct {
		agents  []string
		current string
		older   string
	}{
		{axeAgents("BM1370"), "bitaxe/BM1370/v2.14.2", "bitaxe/BM1370/v2.10.1"},
		{nerdAgents("NerdQAxe++", "BM1370"), "NerdQAxe++/BM1370/V1.0.37.2-LTS", "NerdQAxe++/BM1370/v1.0.35"},
	} {
		counts := make(map[string]int)
		for _, agent := range test.agents {
			counts[agent]++
		}
		if counts[test.current] <= counts[test.older] {
			t.Errorf("current firmware %q is not weighted above %q", test.current, test.older)
		}
	}
}

func TestRandomWorkerNamesArePortable(t *testing.T) {
	for _, sample := range []struct {
		device  string
		profile workerProfile
	}{
		{"bitaxe-gamma-turbo", workerProfileHome},
		{"NerdOCTAXE-γ", workerProfileHome},
		{"antminer-s19-braiins", workerProfileIndustrial},
		{"nicehash-sha256", workerProfileRental},
		{"---", workerProfileHome},
	} {
		seen := make(map[string]bool)
		for range 100 {
			worker, err := randomWorkerName(sample.device, sample.profile)
			if err != nil {
				t.Fatal(err)
			}
			if worker != "" && !workerNamePattern.MatchString(worker) {
				t.Fatalf("worker name is not portable: %q", worker)
			}
			seen[worker] = true
		}
		if len(seen) < 2 {
			t.Fatalf("worker names did not vary for %q: %v", sample.device, seen)
		}
	}
}

func TestWorkerNameStyleWeights(t *testing.T) {
	wants := map[workerProfile]map[workerNameStyle]int{
		workerProfileHome: {
			workerNameBare: 5, workerNameHardwareNumbered: 5, workerNameHardwarePlain: 4,
			workerNameGenericNumbered: 3, workerNameGenericPlain: 2, workerNameNumeric: 1,
		},
		workerProfileIndustrial: {
			workerNameBare: 1, workerNameHardwareNumbered: 4, workerNameHardwarePlain: 2,
			workerNameGenericNumbered: 7, workerNameGenericPlain: 2, workerNameNumeric: 4,
		},
		workerProfileRental: {
			workerNameBare: 1, workerNameHardwareNumbered: 1, workerNameHardwarePlain: 1,
			workerNameGenericNumbered: 5, workerNameGenericPlain: 8, workerNameNumeric: 4,
		},
	}
	for profile, want := range wants {
		counts := make(map[workerNameStyle]int)
		for roll := int64(0); roll < 20; roll++ {
			counts[workerNameStyleForRoll(profile, roll)]++
		}
		for style, wantCount := range want {
			if counts[style] != wantCount {
				t.Errorf("profile %d style %d has %d rolls, want %d", profile, style, counts[style], wantCount)
			}
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
		worker, err := randomWorkerName(preset.worker, preset.profile)
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
