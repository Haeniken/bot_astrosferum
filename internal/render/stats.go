package render

import (
	"fmt"
	"image/color"

	"bot_astrosferum/internal/store"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

func UsageStats(path string, days []store.DailyUsage, language string) error {
	if len(days) == 0 {
		return fmt.Errorf("usage stats are empty")
	}
	p := plot.New()
	options := Options{Language: language}
	p.Title.Text = localized(options, "Запросы прогноза за последние 30 дней", "Forecast requests over the last 30 days")
	p.X.Label.Text = localized(options, "День · MSK (UTC+3)", "Day · MSK (UTC+3)")
	p.Y.Label.Text = localized(options, "Запросы", "Requests")
	p.Y.Min = 0
	successValues := make(plotter.Values, len(days))
	failedValues := make(plotter.Values, len(days))
	labels := make([]string, len(days))
	maximum := float64(1)
	for i, d := range days {
		successValues[i] = float64(d.Successful)
		failedValues[i] = float64(d.Failed)
		if float64(d.Requests) > maximum {
			maximum = float64(d.Requests)
		}
		if i%3 == 0 || i == len(days)-1 {
			labels[i] = d.Day.Format("02.01")
		}
	}
	p.Y.Max = maximum * 1.10
	failedBars, err := plotter.NewBarChart(failedValues, vg.Points(16))
	if err != nil {
		return err
	}
	failedBars.Color = color.RGBA{R: 220, G: 70, B: 70, A: 255}
	successBars, err := plotter.NewBarChart(successValues, vg.Points(16))
	if err != nil {
		return err
	}
	successBars.Color = color.RGBA{R: 55, G: 175, B: 95, A: 255}
	successBars.StackOn(failedBars)
	p.Add(failedBars, successBars)
	p.Legend.Add(localized(options, "С ошибкой", "Failed"), failedBars)
	p.Legend.Add(localized(options, "Успешно", "Successful"), successBars)
	p.Legend.Top = true
	p.NominalX(labels...)
	p.Add(plotter.NewGrid())
	return p.Save(16*vg.Inch, 7*vg.Inch, path)
}
