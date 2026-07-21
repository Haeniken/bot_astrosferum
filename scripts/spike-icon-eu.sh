#!/usr/bin/env bash
set -euo pipefail

project_root=${PROJECT_ROOT:-/work}
data_root=${DATA_ROOT:-"$project_root/data"}
cycle=${ICON_EU_CYCLE:-06}
forecast_step=${ICON_EU_STEP:-003}
base_url=${ICON_EU_BASE_URL:-https://opendata.dwd.de/weather/nwp/icon-eu/grib}

case "$cycle" in
    00|03|06|09|12|15|18|21) ;;
    *)
        printf 'invalid ICON_EU_CYCLE: %s\n' "$cycle" >&2
        exit 2
        ;;
esac

if [[ ! $forecast_step =~ ^[0-9]{3}$ ]]; then
    printf 'ICON_EU_STEP must be a three-digit forecast step\n' >&2
    exit 2
fi
forecast_hour=$((10#$forecast_step))

for command_name in curl bzip2 grib_get grib_ls sha256sum stat awk sed sort; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        printf 'required command is missing: %s\n' "$command_name" >&2
        exit 2
    fi
done

mkdir -p "$data_root/verification/icon-eu-spike"

discover_index=$(curl -fsSL --retry 3 --connect-timeout 10 --max-time 60 \
    "$base_url/$cycle/t_2m/")
latest_name=$(printf '%s\n' "$discover_index" \
    | sed -n 's/.*href="\([^"]*\.grib2\.bz2\)".*/\1/p' \
    | grep '_T_2M\.grib2\.bz2$' \
    | sort -V \
    | tail -n 1)

if [[ ! $latest_name =~ _([0-9]{10})_[0-9]{3}_T_2M\.grib2\.bz2$ ]]; then
    printf 'could not discover the latest ICON-EU run from %s/%s/t_2m/\n' \
        "$base_url" "$cycle" >&2
    exit 1
fi
run_id=${BASH_REMATCH[1]}

run_dir="$data_root/verification/icon-eu-spike/$run_id/f$forecast_step"
raw_dir="$run_dir/raw"
grib_dir="$run_dir/grib"
report_dir="$run_dir/report"
mkdir -p "$raw_dir" "$grib_dir" "$report_dir"

inventory="$report_dir/inventory.tsv"
metadata="$report_dir/grib-metadata.tsv"
point_samples="$report_dir/point-samples.tsv"
nearest_cells="$report_dir/nearest-cells.tsv"
vertical_heights="$report_dir/vertical-heights.tsv"
selected_levels="$report_dir/selected-model-levels.tsv"
report="$report_dir/report.md"
started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
started_epoch=$(date +%s)

printf 'category\tparameter\tlevel\turl\tcompressed_bytes\tgrib_bytes\tsha256\n' > "$inventory"

download_grib() {
    local category=$1
    local parameter=$2
    local level=$3
    local url=$4
    local filename=${url##*/}
    local compressed="$raw_dir/$filename"
    local compressed_part="$compressed.part"
    local grib="$grib_dir/${filename%.bz2}"
    local grib_part="$grib.part"

    if [[ ! -s $compressed ]]; then
        printf 'download %s %s level=%s\n' "$category" "$parameter" "$level"
        curl -fL --retry 3 --retry-delay 2 --connect-timeout 10 --max-time 180 \
            -o "$compressed_part" "$url"
        mv "$compressed_part" "$compressed"
    fi

    bzip2 -t "$compressed"
    if [[ ! -s $grib ]]; then
        bzip2 -dc "$compressed" > "$grib_part"
        mv "$grib_part" "$grib"
    fi

    local compressed_bytes grib_bytes digest
    compressed_bytes=$(stat -c %s "$compressed")
    grib_bytes=$(stat -c %s "$grib")
    digest=$(sha256sum "$compressed" | awk '{print $1}')
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$category" "$parameter" "$level" "$url" \
        "$compressed_bytes" "$grib_bytes" "$digest" >> "$inventory"
}

surface_fields=(
    't_2m:T_2M'
    'td_2m:TD_2M'
    'relhum_2m:RELHUM_2M'
    'clcl:CLCL'
    'clcm:CLCM'
    'clch:CLCH'
    'clct:CLCT'
    'tot_prec:TOT_PREC'
    'u_10m:U_10M'
    'v_10m:V_10M'
    'vmax_10m:VMAX_10M'
    'pmsl:PMSL'
    'ps:PS'
    't_g:T_G'
)

for field in "${surface_fields[@]}"; do
    parameter=${field%%:*}
    code=${field##*:}
    filename="icon-eu_europe_regular-lat-lon_single-level_${run_id}_${forecast_step}_${code}.grib2.bz2"
    download_grib surface "$parameter" surface "$base_url/$cycle/$parameter/$filename"
done

model_levels=(1 20 40 60 74)
upper_fields=(
    'u:U'
    'v:V'
    't:T'
    'p:P'
    'qv:QV'
    'tke:TKE'
)

for field in "${upper_fields[@]}"; do
    parameter=${field%%:*}
    code=${field##*:}
    for level in "${model_levels[@]}"; do
        filename="icon-eu_europe_regular-lat-lon_model-level_${run_id}_${forecast_step}_${level}_${code}.grib2.bz2"
        download_grib upper "$parameter" "$level" "$base_url/$cycle/$parameter/$filename"
    done
done

mapfile -t hhl_levels < <(seq 1 75)
for level in "${hhl_levels[@]}"; do
    filename="icon-eu_europe_regular-lat-lon_time-invariant_${run_id}_${level}_HHL.grib2.bz2"
    download_grib static hhl "$level" "$base_url/$cycle/hhl/$filename"
done

printf 'file\tshortName\ttypeOfLevel\tlevel\tstep\tstepUnits\tstepRange\tdataDate\tdataTime\tvalidityDate\tvalidityTime\tNi\tNj\tlat_first\tlon_first\tlat_last\tlon_last\tdi\tdj\tunits\n' > "$metadata"
while IFS= read -r grib_file; do
    values=$(grib_get -f -p \
shortName,typeOfLevel,level,step,stepUnits:s,stepRange,dataDate,dataTime,validityDate,validityTime,Ni,Nj,latitudeOfFirstGridPointInDegrees,longitudeOfFirstGridPointInDegrees,latitudeOfLastGridPointInDegrees,longitudeOfLastGridPointInDegrees,iDirectionIncrementInDegrees,jDirectionIncrementInDegrees \
        "$grib_file" | tr -s ' ' '\t')
    units=$(grib_get -f -p units "$grib_file")
    printf '%s\t%s\t%s\n' "$(basename "$grib_file")" "$values" "$units" >> "$metadata"
done < <(find "$grib_dir" -maxdepth 1 -type f -name '*.grib2' | sort)

cities=(
    'saint_petersburg:59.9386:30.3141'
    'moscow:55.7558:37.6173'
)

printf 'city\trequested_lat\trequested_lon\tgrid_index\tgrid_lat\tgrid_lon\tdistance_km\n' > "$nearest_cells"
reference_grib=$(find "$grib_dir" -maxdepth 1 -type f -name "*_${forecast_step}_T_2M.grib2" -print -quit)
if [[ -z $reference_grib ]]; then
    printf 'reference T_2M GRIB was not found\n' >&2
    exit 1
fi

for city_spec in "${cities[@]}"; do
    city=${city_spec%%:*}
    coordinates=${city_spec#*:}
    latitude=${coordinates%%:*}
    longitude=${coordinates##*:}
    chosen=$(grib_ls -l "$latitude,$longitude,1" "$reference_grib" 2>&1 \
        | sed -n 's/.*Grid Point chosen #[0-9][0-9]* index=\([0-9][0-9]*\) latitude=\([^ ]*\) longitude=\([^ ]*\) distance=\([^ ]*\).*/\1\t\2\t\3\t\4/p' \
        | head -n 1)
    if [[ -z $chosen ]]; then
        printf 'could not resolve nearest grid cell for %s\n' "$city" >&2
        exit 1
    fi
    printf '%s\t%s\t%s\t%s\n' "$city" "$latitude" "$longitude" "$chosen" >> "$nearest_cells"
done

printf 'city\trequested_lat\trequested_lon\tfile\tshortName\ttypeOfLevel\tlevel\tencoded_step\tvalue\n' > "$point_samples"

while IFS= read -r grib_file; do
    for city_spec in "${cities[@]}"; do
        city=${city_spec%%:*}
        coordinates=${city_spec#*:}
        latitude=${coordinates%%:*}
        longitude=${coordinates##*:}
        values=$(grib_get -f -F '%.8g' \
            -p shortName,typeOfLevel,level,step \
            -l "$latitude,$longitude,1" "$grib_file" | tr -s ' ' '\t')
        printf '%s\t%s\t%s\t%s\t%s\n' \
            "$city" "$latitude" "$longitude" "$(basename "$grib_file")" "$values" \
            >> "$point_samples"
    done
done < <(find "$grib_dir" -maxdepth 1 -type f -name '*.grib2' | sort)

printf 'city\thalf_level\thhl_m\n' > "$vertical_heights"
awk -F '\t' 'NR > 1 && $5 == "HHL" {print $1 "\t" $7 "\t" $9}' \
    "$point_samples" | sort -t $'\t' -k1,1 -k2,2n >> "$vertical_heights"

printf 'target_agl_m\tmodel_level\tmean_agl_m\tmin_agl_m\tmax_agl_m\n' > "$selected_levels"
awk -F '\t' '
    NR > 1 {
        height[$1 SUBSEP $2] = $3
        cities[$1] = 1
    }
    END {
        for (level = 1; level <= 74; level++) {
            count = 0
            sum = 0
            min_height = 1e30
            max_height = -1e30
            for (city in cities) {
                full_height = (height[city SUBSEP level] + height[city SUBSEP (level + 1)]) / 2 - height[city SUBSEP 75]
                sum += full_height
                count++
                if (full_height < min_height) min_height = full_height
                if (full_height > max_height) max_height = full_height
            }
            mean[level] = sum / count
            minimum[level] = min_height
            maximum[level] = max_height
        }

        target_count = split("0 100 200 300 400 500 650 800 1000 1200 1400 1600 1800 2000 2400 2800 3200 3600 4200 4800 5500 6300 7200 8000 9000 10000 11000 12000 13000 14000 15000 17000 19000 21000 22500", targets, " ")
        for (target_index = 1; target_index <= target_count; target_index++) {
            target = targets[target_index]
            best_level = 0
            best_delta = 1e30
            for (level = 1; level <= 74; level++) {
                if (used[level]) continue
                delta = mean[level] - target
                if (delta < 0) delta = -delta
                if (delta < best_delta) {
                    best_delta = delta
                    best_level = level
                }
            }
            used[best_level] = 1
            printf "%.0f\t%d\t%.1f\t%.1f\t%.1f\n", target, best_level, mean[best_level], minimum[best_level], maximum[best_level]
        }
    }
' "$vertical_heights" >> "$selected_levels"

completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
completed_epoch=$(date +%s)
duration_seconds=$((completed_epoch - started_epoch))
file_count=$(awk 'END {print NR-1}' "$inventory")
compressed_bytes=$(awk -F '\t' 'NR > 1 {sum += $5} END {printf "%.0f", sum}' "$inventory")
grib_bytes=$(awk -F '\t' 'NR > 1 {sum += $6} END {printf "%.0f", sum}' "$inventory")
surface_average=$(awk -F '\t' '$1 == "surface" {sum += $5; n++} END {printf "%.0f", n ? sum/n : 0}' "$inventory")
upper_average=$(awk -F '\t' '$1 == "upper" {sum += $5; n++} END {printf "%.0f", n ? sum/n : 0}' "$inventory")
static_average=$(awk -F '\t' '$1 == "static" {sum += $5; n++} END {printf "%.0f", n ? sum/n : 0}' "$inventory")

# MVP estimate: 14 surface fields hourly through +72 h, 6 dynamic upper
# fields on 35 selected levels every 3 h, and 35 static HHL levels.
estimated_surface_files=$((14 * 73))
estimated_upper_files=$((6 * 35 * 25))
estimated_static_files=35
estimated_run_bytes=$((
    surface_average * estimated_surface_files
    + upper_average * estimated_upper_files
    + static_average * estimated_static_files
))

eccodes_version=$(grib_get -V 2>&1 | sed -n '/[^[:space:]]/p' | head -n 1)

cat > "$report" <<EOF
# ICON-EU server-side data-source spike

- Started: $started_at
- Completed: $completed_at
- Run: $run_id
- Cycle directory: $cycle UTC
- Forecast step sampled: +$forecast_hour h
- Model levels sampled: ${model_levels[*]}
- HHL half-levels sampled: 1..75
- Files validated: $file_count
- Downloaded compressed bytes: $compressed_bytes
- Expanded GRIB bytes: $grib_bytes
- Duration: $duration_seconds seconds
- ecCodes: $eccodes_version

## Rough MVP run estimate

- Surface files: $estimated_surface_files
- Dynamic upper-air files: $estimated_upper_files
- Static HHL files: $estimated_static_files
- Total files: $((estimated_surface_files + estimated_upper_files + estimated_static_files))
- Estimated compressed bytes: $estimated_run_bytes

This is a sample-based capacity estimate, not a measured full-run download. File
sizes vary by parameter, level, and forecast step. A later controlled full sync
must enforce the configured free-space threshold.

## Artifacts

- inventory.tsv: URLs, sizes, and compressed-file SHA-256.
- grib-metadata.tsv: ecCodes keys, grid geometry, units, levels, and steps.
- point-samples.tsv: nearest-point extraction for the four priority cities.
- nearest-cells.tsv: selected regular-grid index, coordinates, and distance.
- vertical-heights.tsv: HHL height for every half-level at every priority city.
- selected-model-levels.tsv: 35 levels selected by target AGL height.
- raw/: downloaded bzip2 files.
- grib/: decompressed GRIB2 samples.
EOF

printf 'spike complete: %s\n' "$report"
printf 'run=%s files=%s compressed_bytes=%s grib_bytes=%s estimated_run_bytes=%s duration_seconds=%s\n' \
    "$run_id" "$file_count" "$compressed_bytes" "$grib_bytes" \
    "$estimated_run_bytes" "$duration_seconds"
