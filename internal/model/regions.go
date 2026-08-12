package model

import (
	_ "embed"
	"encoding/json"
	"sort"
)

// ProductionRegion is the shared deployment and reporting contract used by
// StratumStats and StratumScout. Keep this file byte-for-byte synchronized
// between the two repositories with StratumScout's check-core-sync script.
type ProductionRegion struct {
	Region    string `json:"code"`
	City      string `json:"city"`
	Country   string `json:"country"`
	Enabled   bool   `json:"enabled"`
	Gateway   bool   `json:"gateway"`
	Vantage   string `json:"vantage,omitempty"`
	Label     string `json:"label,omitempty"`
	Continent string `json:"continent,omitempty"`
	Order     int    `json:"order,omitempty"`
}

//go:embed regions.json
var productionRegionsJSON []byte

var productionRegions = loadProductionRegions()

func loadProductionRegions() []ProductionRegion {
	var regions []ProductionRegion
	if err := json.Unmarshal(productionRegionsJSON, &regions); err != nil || len(regions) == 0 {
		panic("invalid embedded production region registry")
	}
	seenRegions, seenVantages := make(map[string]bool), make(map[string]bool)
	for _, region := range regions {
		if region.Region == "" || region.City == "" || region.Country == "" || seenRegions[region.Region] ||
			(region.Enabled && (region.Vantage == "" || region.Label == "" || region.Continent == "" || seenVantages[region.Vantage])) {
			panic("invalid embedded production region registry")
		}
		seenRegions[region.Region] = true
		if region.Enabled {
			seenVantages[region.Vantage] = true
		}
	}
	return regions
}

func ProductionRegions() []ProductionRegion {
	enabled := make([]ProductionRegion, 0, len(productionRegions))
	for _, region := range productionRegions {
		if region.Enabled {
			enabled = append(enabled, region)
		}
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Order < enabled[j].Order })
	return enabled
}

func ProductionRegionForCode(code string) (ProductionRegion, bool) {
	for _, region := range productionRegions {
		if region.Enabled && region.Region == code {
			return region, true
		}
	}
	return ProductionRegion{}, false
}

func ProductionRegionForVantage(vantage string) (ProductionRegion, bool) {
	for _, region := range productionRegions {
		if region.Enabled && region.Vantage == vantage {
			return region, true
		}
	}
	return ProductionRegion{}, false
}
