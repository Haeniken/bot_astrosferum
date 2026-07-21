package model

import (
	"context"
	"time"

	"bot_astrosferum/internal/forecast"
)

type Coverage struct {
	GridType  string
	GridName  string
	MinLat    float64
	MaxLat    float64
	MinLon    float64
	MaxLon    float64
	Increment float64
}

func (c Coverage) Contains(location forecast.Location) bool {
	return location.Latitude >= c.MinLat && location.Latitude <= c.MaxLat &&
		location.Longitude >= c.MinLon && location.Longitude <= c.MaxLon
}

type RemoteRun struct {
	ID       string
	BaseTime time.Time
}

type Manifest struct {
	Provider string
	RunID    string
	BaseTime time.Time
	Grid     Coverage
	Complete bool
}

type PointSeries struct {
	Location forecast.Location
	Cell     GridCell
	Samples  []Sample
}

type Provider interface {
	Name() string
	Coverage() Coverage
	ProbeLatest(context.Context) (RemoteRun, error)
	Sync(context.Context, RemoteRun, string) (Manifest, error)
	ExtractPoint(context.Context, Manifest, forecast.Location) (PointSeries, error)
}

type GridCell struct {
	Index      int     `json:"index"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	DistanceKM float64 `json:"distance_km"`
}

type Sample struct {
	File        string  `json:"file"`
	ShortName   string  `json:"short_name"`
	TypeOfLevel string  `json:"type_of_level"`
	Level       float64 `json:"level"`
	Step        int     `json:"step"`
	StepUnits   string  `json:"step_units"`
	StepRange   string  `json:"step_range"`
	Units       string  `json:"units"`
	Value       float64 `json:"value"`
}
