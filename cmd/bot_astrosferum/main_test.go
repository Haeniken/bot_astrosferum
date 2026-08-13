package main

import (
	"testing"

	"bot_astrosferum/internal/config"
)

func TestDirectionalVolumeRequiredForHorizonIndependentlyOfAstrodomeRollout(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "everything disabled", cfg: config.Config{}, want: false},
		{name: "Horizon enabled", cfg: config.Config{HorizonAnalysis: config.HorizonAnalysisConfig{Enabled: true}}, want: true},
		{name: "Astrodome enabled", cfg: config.Config{Astrodome: config.AstrodomeConfig{Enabled: true}}, want: true},
		{name: "administrator preview", cfg: config.Config{Platforms: config.PlatformsConfig{Telegram: config.PlatformConfig{AdminIDs: []int64{1}}}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := directionalVolumeRequired(test.cfg); got != test.want {
				t.Fatalf("directionalVolumeRequired() = %v; want %v", got, test.want)
			}
		})
	}
}
