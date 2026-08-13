package bot

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

func startHelp(language userLanguage, includeHorizon bool) string {
	help := StartHelp
	marker := "\n\nЗасветка:"
	if language == languageEnglish {
		help = StartHelpEN
		marker = "\n\nLight pollution:"
	}
	if !includeHorizon {
		return help
	}
	item := language.text(
		"8. «Горизонт» (только ICON-EU) — рефракционный расчёт f001…f072 на видимой высоте 10° по 8 азимутам: индекс, главный ограничитель и качество данных.",
		"8. “Horizon” (ICON-EU only) — a refracted f001…f072 calculation at 10° apparent elevation in eight azimuths: index, principal limiter, and input-data quality.",
	)
	return strings.Replace(help, marker, "\n\n"+item+marker, 1)
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

Outside ICON-EU, the latest complete ICON Global run uses the same model-level profile. Global TKE is available through +48 h, so its hybrid Overall chart is shorter than the other charts.

How to read the result:

Altitude charts: horizontal axis is local time; left axis is pressure, right axis is ICON height (850 hPa ≈ 1.5 km); lighter colors mean larger values, see the scale below each map.

1. Hourly weather for 72 hours: conditions, wind, humidity, T−Td, a fog heuristic, and celestial events. A drop means dew protection may be useful; dew does not imply poor seeing. “Transparency heuristic” is a ranking aid based on clouds, VIS, and PWV, not optical transmission.

Cloud layers and astronomy:
• low clouds block targets and reflect light pollution;
• middle clouds reduce signal and contrast and create an uneven background;
• high clouds raise the sky background and harm long exposures and photometry;
• in the weather table, white is <10%, blue is 10–49%, and orange is ≥50%; this is cover, not optical thickness;
• clouds do not change wind-based seeing, but can prevent observing or imaging.

2. Overall Astronomy Index, 1–10: retained suitability is at the bottom; above it, an exact additive decomposition of losses from optical turbulence, cloud obstruction, wind, fog, and precipitation closes every column at 10. Precipitation above the configured threshold is a red operational veto and forces index 1; “!” means incomplete inputs. Hybrid seeing uses ICON TKE in the boundary layer plus HMNSP99 above it. Surface wind has a mild penalty; dew has none.

The “Reference V, zenith” ring is shown only during astronomical night. It accounts for PWV, AOD, O₃, the Moon, and PSF/seeing, but not artificial light. If GEOS-CF is unavailable, the ring is omitted while the main Overall chart remains available.

3. ICON Effective Cloud Obstruction — CLC+QC/QI and layer thickness: 0% is nearly clear, 100% is opaque; thin high clouds matter less than dense low clouds.

4. Wind Speed, m/s — wind by altitude. Strong flow at 300–200 hPa often indicates a jet stream; surface wind can shake a telescope.

5. Vector Wind Shear, m/s/km — vector shear normalized by actual vertical distance; higher values imply more turbulence risk.

6. Wind Direction Delta, ° — wind rotation between adjacent levels. It is shown as 0° below 2 m/s because near-calm direction is unstable and has little observational impact.

7. Forecast Wind Seeing Index — wind-based estimate from 1 to 10. The percentage is a lead-time quality heuristic, not statistical confidence, and is not part of the Overall Index.

Light pollution: coordinate-specific LPI/SQM from Atlas 2024 and a separate World Atlas 2015 comparison. Bortle is a zenith-oriented reference and is not part of the Overall Index.

Times use the location’s time zone. Forecast seeing is a model estimate, not a local DIMM measurement or the Pickering scale.`

const NotReadyTextEN = `The coordinates were recognized, but the live ICON forecast pipeline is still being prepared. I do not send synthetic data to users.`
