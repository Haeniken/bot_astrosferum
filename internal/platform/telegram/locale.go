package telegram

import "strings"

type userLanguage string

const (
	languageRussian userLanguage = "ru"
	languageEnglish userLanguage = "en"
)

func languageFromCode(code string) userLanguage {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "ru" || strings.HasPrefix(code, "ru-") || strings.HasPrefix(code, "ru_") {
		return languageRussian
	}
	return languageEnglish
}

func (language userLanguage) text(russian, english string) string {
	if language == languageEnglish {
		return english
	}
	return russian
}

func (language userLanguage) renderCode() string {
	if language == languageEnglish {
		return "en"
	}
	return "ru"
}

func startHelp(language userLanguage) string {
	if language == languageEnglish {
		return StartHelpEN
	}
	return StartHelp
}

func defaultKeyboard(language userLanguage) Keyboard {
	if language == languageEnglish {
		return Keyboard{{{Text: "📍 Share location", RequestLocation: true}}, {{Text: "💾 Save coordinates"}, {Text: "📌 My locations"}}}
	}
	return DefaultKeyboard()
}

func isButton(text, russian, english string) bool {
	return text == russian || text == english
}

const StartHelpEN = `Hello! I build an astronomy forecast for a selected location.

How to request a forecast:
• tap “📍 Share location”;
• send coordinates as text: 59.9386, 30.3141;
• use: /forecast 59.9386 30.3141;
• “💾 Save coordinates” stores up to 10 locations; open “📌 My locations” to select one, or use /deletepoint N to delete it.

How to read the result:

Altitude charts: horizontal axis is local time; left axis is pressure, right axis is ICON height (850 hPa ≈ 1.5 km); lighter colors mean larger values, see the scale below each map.

1. Hourly weather for 72 hours: conditions, wind, humidity, T−Td, fog, and celestial events. A drop means dew protection may be useful; dew does not imply poor seeing. “Transparency %” is a comparative proxy based on clouds, VIS, and PWV, not measured extinction.

Cloud layers and astronomy:
• low clouds block targets and reflect light pollution;
• middle clouds reduce signal and contrast and create an uneven background;
• high clouds raise the sky background and harm long exposures and photometry;
• in the weather table, white is <10%, blue is 10–49%, and orange is ≥50%; this is cover, not optical thickness;
• clouds do not change wind-based seeing, but can prevent observing or imaging.

2. Overall Astronomy Index — suitability from 1 to 10: hybrid ICON seeing (TKE up to dynamic MH 500–2000 m AGL + HMNSP99 above), τ₀, effective cloud obstruction, and fog. Surface wind has a mild penalty; dew has none. MH is the hourly ICON mixed-layer depth. Labels show seeing / τ₀ ms / T% transmission; f/F means possible/high fog. Background means day/twilight/night.

3. ICON Effective Cloud Obstruction — CLC+QC/QI and layer thickness: 0% is nearly clear, 100% is opaque; thin high clouds matter less than dense low clouds.

4. Wind Speed, m/s — wind by altitude. Strong flow at 300–200 hPa often indicates a jet stream; surface wind can shake a telescope.

5. Vector Wind Shear, m/s/km — vector shear normalized by actual vertical distance; higher values imply more turbulence risk.

6. Wind Direction Delta, ° — wind rotation between adjacent levels. It is shown as 0° below 2 m/s because near-calm direction is unstable and has little observational impact.

7. Forecast Wind Seeing Index — wind-based estimate from 1 to 10. The percentage is conditional confidence based only on lead time and is not part of the Overall Index.

Light pollution: coordinate-specific LPI/SQM from Atlas 2024 and a separate World Atlas 2015 comparison. Bortle is a zenith-oriented reference and is not part of the Overall Index.

Times use the location’s time zone. Forecast seeing is a model estimate, not a local DIMM measurement or the Pickering scale.`

const NotReadyTextEN = `The coordinates were recognized, but the live ICON forecast pipeline is still being prepared. I do not send synthetic data to users.`
