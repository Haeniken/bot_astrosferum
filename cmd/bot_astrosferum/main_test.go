package main

import (
	"testing"

	"bot_astrosferum/internal/app/directional"
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

func TestTelegramCompletionMessageContainsOnlyTerminalStatus(t *testing.T) {
	tests := []struct {
		language string
		kind     string
		state    directional.State
		want     string
	}{
		{language: "ru", kind: "astrodome", state: directional.StateReady, want: "Астрокупол: результат готов на сайте Astrosferum."},
		{language: "en", kind: "horizon", state: directional.StateFailed, want: "Horizon: the calculation failed. Open the Astrosferum website to check the job status."},
	}
	for _, test := range tests {
		if got := telegramCompletionMessage(test.language, test.kind, test.state); got != test.want {
			t.Errorf("telegramCompletionMessage(%q, %q, %q) = %q, want %q", test.language, test.kind, test.state, got, test.want)
		}
	}
}
