# `.env` configuration reference

[Русская версия](configuration.ru.md)

Copy `.env.example` to the ignored `.env`. Only `POSTGRES_PASSWORD` is required
by Compose; every other variable is optional. Platform tokens deliberately do
not belong in `.env`: keep them in `secrets/telegram_token` and
`secrets/vk_token` with mode `0600`. `ASTRO_DB_PASSWORD` is populated inside
the application container from `POSTGRES_PASSWORD` and should not be set
separately.

## Runtime and access

| Variable | Required | Recommended value | Effect |
|---|---|---|---|
| `POSTGRES_PASSWORD` | Yes | A unique random value of at least 32 characters | PostgreSQL and application database password. Changing it after database initialization also requires changing the database role password. |
| `POSTGRES_DB` | No | `bot_astrosferum` | PostgreSQL database name. Keep the default unless integrating with an existing PostgreSQL installation. |
| `POSTGRES_USER` | No | `bot_astrosferum` | PostgreSQL role name. Keep the default for the bundled Compose deployment. |
| `ASTRO_TELEGRAM_ADMIN_IDS` | No | Comma-separated numeric IDs, or empty | Grants the Telegram `/admin` and statistics UI. It does not filter statistics: every configured admin sees the combined Telegram + VK totals. |
| `ASTRO_VK_ADMIN_IDS` | No | Comma-separated numeric VK user IDs, or empty | Grants the VK admin/statistics UI. Use numeric user IDs, not screen names; the report is the same combined Telegram + VK report. |
| `ASTRO_LIGHT_POLLUTION_ATLAS_YEAR` | No | `2024` | Pins the operator-verified annual Light Pollution Atlas dataset; it is not advanced automatically. Change only after verifying and provisioning a supported dataset. |

## Overall Astronomy Index calibration

The remaining optional variables calibrate the Overall Astronomy Index. The
repository defaults below are the recommended production profile; change them
as one reviewed calibration set rather than tuning individual values casually.
Invalid or inconsistent values stop the application during configuration
validation.

| Variable | Recommended | Effect |
|---|---:|---|
| `ASTRO_OVERALL_SEEING_WEIGHT` | `1.0` | Exponent applied to optical-seeing quality; larger values penalize mediocre seeing more strongly. |
| `ASTRO_OVERALL_CLOUD_WEIGHT` | `2.0` | Exponent applied to modeled cloud transmission; larger values make cloud obstruction dominate more strongly. |
| `ASTRO_OVERALL_COHERENCE_TIME_WEIGHT` | `0.25` | Bounded weight of wind-sensitive coherence time `tau0`; `0` disables this guard and `1` applies it fully. |
| `ASTRO_OVERALL_POSSIBLE_FOG_FACTOR` / `ASTRO_OVERALL_HIGH_FOG_FACTOR` | `0.75` / `0.10` | Multipliers for possible/high fog. Smaller factors impose a stronger penalty. |
| `ASTRO_OVERALL_GOOD_SEEING_ARCSEC` / `ASTRO_OVERALL_BAD_SEEING_ARCSEC` | `0.5` / `2.0` | Best and poor reference limits for logarithmically mapping modeled seeing to quality. |
| `ASTRO_OVERALL_BEST_COHERENCE_TIME_MS` / `ASTRO_OVERALL_BAD_COHERENCE_TIME_MS` | `5.2` / `1.6` | Best and poor reference limits for mapping `tau0` to its guard factor. |
| `ASTRO_OVERALL_BOUNDARY_LAYER_MIN_M` / `ASTRO_OVERALL_BOUNDARY_LAYER_TOP_M` | `500` / `2000` | Lower/upper clamps for the ICON mixed-layer depth used by the hybrid TKE + HMNSP99 turbulence integration. |
| `ASTRO_OVERALL_GROUND_CN2_SCALE` | `1.0` | Multiplier for ground-layer `Cn2`; values above `1` increase the modeled near-ground turbulence contribution. |
| `ASTRO_OVERALL_UNRESOLVED_CLOUD_OBSTRUCTION` | `0.45` | Maximum low-cloud diagnostic guard when `CLC` is not represented by resolved `QC/QI`; middle/high guards are smaller fixed fractions. |
| `ASTRO_OVERALL_SURFACE_WIND_MAX_PENALTY` | `0.20` | Maximum fractional surface-wind/gust penalty; `0.20` preserves at least an `0.80` factor from this guard alone. |
| `ASTRO_OVERALL_SURFACE_WIND_START_MS` / `ASTRO_OVERALL_SURFACE_WIND_FULL_MS` | `8.5` / `15.0` | Sustained-wind speeds where the mild surface penalty starts and reaches its configured maximum. |
| `ASTRO_OVERALL_SURFACE_GUST_START_MS` / `ASTRO_OVERALL_SURFACE_GUST_FULL_MS` | `12.0` / `22.0` | Gust speeds where the same penalty starts and reaches its maximum. |
| `ASTRO_CLOUD_LIQUID_RADIUS_MICROMETERS` / `ASTRO_CLOUD_ICE_RADIUS_MICROMETERS` | `10` / `25` | Assumed effective droplet/crystal radii used to convert liquid/ice water path into visible optical depth. Smaller radii imply stronger extinction for the same condensate mass. |

Keep `.env` outside Git. After changing a value, validate the effective
configuration and recreate the application container:

```sh
docker compose run --rm bot_astrosferum doctor --config /app/config/config.yaml
docker compose up -d --no-deps --force-recreate bot_astrosferum
```
