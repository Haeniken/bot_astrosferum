package forecast

import "time"

// AtmosphericCompositionSeries keeps the independently initialized
// composition forecast separate from the ICON meteorological run while giving
// the application one provider-neutral contract.
type AtmosphericCompositionSeries struct {
	Location     Location                      `json:"location"`
	Provider     string                        `json:"provider"`
	Product      string                        `json:"product"`
	RunID        string                        `json:"run_id"`
	BaseTime     time.Time                     `json:"base_time"`
	RetrievedAt  time.Time                     `json:"retrieved_at"`
	Grid         string                        `json:"grid"`
	DatasetURL   string                        `json:"dataset_url"`
	FreshnessAge time.Duration                 `json:"freshness_age"`
	Frames       []AtmosphericCompositionFrame `json:"frames"`
}

func (series AtmosphericCompositionSeries) FrameAt(at time.Time) (*AtmosphericCompositionFrame, bool) {
	for index := range series.Frames {
		if series.Frames[index].ValidAt.Equal(at) {
			return &series.Frames[index], true
		}
	}
	return nil, false
}
