package main

import (
	"testing"

	"bot_astrosferum/internal/config"
)

func TestAstrodomeVolumeRequiredIndependentlyOfStraightRayHorizon(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "everything disabled", cfg: config.Config{}, want: false},
		{name: "straight-ray Horizon enabled", cfg: config.Config{HorizonAnalysis: config.HorizonAnalysisConfig{Enabled: true}}, want: false},
		{name: "Astrodome enabled", cfg: config.Config{Astrodome: config.AstrodomeConfig{Enabled: true}}, want: true},
		{name: "administrator preview", cfg: config.Config{Platforms: config.PlatformsConfig{Telegram: config.PlatformConfig{AdminIDs: []int64{1}}}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := astrodomeVolumeRequired(test.cfg); got != test.want {
				t.Fatalf("astrodomeVolumeRequired() = %v; want %v", got, test.want)
			}
		})
	}
}
