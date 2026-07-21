package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", text, err)
	}
	d.Duration = value
	return nil
}

type ByteSize int64

func (s *ByteSize) UnmarshalText(text []byte) error {
	value, err := parseByteSize(string(text))
	if err != nil {
		return err
	}
	*s = ByteSize(value)
	return nil
}

func parseByteSize(input string) (int64, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, fmt.Errorf("byte size is empty")
	}

	units := []struct {
		suffix     string
		multiplier int64
	}{
		{"TiB", 1 << 40},
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"TB", 1_000_000_000_000},
		{"GB", 1_000_000_000},
		{"MB", 1_000_000},
		{"KB", 1_000},
		{"B", 1},
	}

	for _, unit := range units {
		if !strings.HasSuffix(trimmed, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
		parsed, err := strconv.ParseInt(number, 10, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("invalid byte size %q", input)
		}
		if parsed > (1<<63-1)/unit.multiplier {
			return 0, fmt.Errorf("byte size %q overflows int64", input)
		}
		return parsed * unit.multiplier, nil
	}

	return 0, fmt.Errorf("byte size %q must include B, KiB, MiB, GiB, or TiB", input)
}
