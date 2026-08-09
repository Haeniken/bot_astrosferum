package forecast

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// AstrodomeFrameCount is the maximum version-one web-product time
	// cardinality. A current run normally supplies all 72 consecutive hourly
	// frames; an ageing run may stop earlier at the first native ICON cadence
	// gap rather than synthesising a missing hour.
	AstrodomeFrameCount = 72

	AstrodomeMinimumElevationDegrees = 10.0
	AstrodomeZenithElevationDegrees  = 90.0
	AstrodomeGridGeometryVersion     = "astrosferum-grid-geometry-v1"
	AstrodomeGridTessellationVersion = "spherical-voronoi-v1"
)

// AstrodomeGridProfileID identifies an immutable angular sampling contract.
// A profile change is a schema change: rings must not be thinned within a
// dataset after admission.
type AstrodomeGridProfileID string

const (
	AstrodomeGridDenseV1         AstrodomeGridProfileID = "dense-v1"
	AstrodomeGridSparseStorageV1 AstrodomeGridProfileID = "sparse-storage-v1"
	// AstrodomeGridProductionV2 is the bounded interactive contract: sixteen
	// exact azimuths on every 10-degree elevation ring plus one azimuth-free
	// zenith. It preserves hourly physical recomputation while avoiding the
	// redundant sub-ring oversampling of dense-v1.
	AstrodomeGridProductionV2 AstrodomeGridProfileID = "production-v2"
)

// AstrodomeGridRing is one tilted elevation ring. Azimuths start at north and
// advance eastward by AzimuthStepDegrees. The step is the exact quotient
// 360/AzimuthCount; displayed decimal values may be rounded independently.
type AstrodomeGridRing struct {
	ElevationDegrees   float64 `json:"elevation_deg"`
	AzimuthCount       int     `json:"azimuth_count"`
	AzimuthStepDegrees float64 `json:"azimuth_step_deg"`
}

// AstrodomeGridProfile contains tilted rings only. The single 90-degree
// zenith node is an invariant of every supported profile and is appended by
// Nodes with an undefined (nil) azimuth.
type AstrodomeGridProfile struct {
	ID    AstrodomeGridProfileID `json:"id"`
	Rings []AstrodomeGridRing    `json:"rings"`
}

// AstrodomeGridNode is one canonical angular node. RingIndex and
// AzimuthIndex are -1 at the zenith; AzimuthDegrees is nil there so no
// arbitrary bearing is attached to a physically azimuth-independent point.
type AstrodomeGridNode struct {
	Index            int      `json:"index"`
	RingIndex        int      `json:"ring_index"`
	AzimuthIndex     int      `json:"azimuth_index"`
	ElevationDegrees float64  `json:"elevation_deg"`
	AzimuthDegrees   *float64 `json:"azimuth_deg"`
}

type astrodomeRingContract struct {
	elevationDegrees float64
	azimuthCount     int
}

func astrodomeDenseV1RingContract() []astrodomeRingContract {
	return []astrodomeRingContract{
		{10.000, 60},
		{11.343, 52},
		{13.035, 44},
		{15.236, 40},
		{18.220, 32},
		{20.000, 32},
		{22.481, 28},
		{30.000, 20},
		{40.000, 16},
		{50.000, 12},
		{60.000, 8},
		{70.000, 4},
		{80.000, 4},
	}
}

func astrodomeSparseStorageV1RingContract() []astrodomeRingContract {
	return []astrodomeRingContract{
		{10, 16},
		{20, 16},
		{30, 16},
		{45, 16},
		{60, 16},
		{75, 16},
	}
}

func astrodomeProductionV2RingContract() []astrodomeRingContract {
	return []astrodomeRingContract{
		{10, 16},
		{20, 16},
		{30, 16},
		{40, 16},
		{50, 16},
		{60, 16},
		{70, 16},
		{80, 16},
	}
}

// NewAstrodomeGridProfile returns a fresh copy of a supported versioned grid.
func NewAstrodomeGridProfile(id AstrodomeGridProfileID) (AstrodomeGridProfile, error) {
	switch id {
	case AstrodomeGridDenseV1:
		return newAstrodomeGridProfile(id, astrodomeDenseV1RingContract()), nil
	case AstrodomeGridSparseStorageV1:
		return newAstrodomeGridProfile(id, astrodomeSparseStorageV1RingContract()), nil
	case AstrodomeGridProductionV2:
		return newAstrodomeGridProfile(id, astrodomeProductionV2RingContract()), nil
	default:
		return AstrodomeGridProfile{}, fmt.Errorf("unsupported astrodome grid profile %q", id)
	}
}

func newAstrodomeGridProfile(id AstrodomeGridProfileID, contract []astrodomeRingContract) AstrodomeGridProfile {
	profile := AstrodomeGridProfile{
		ID:    id,
		Rings: make([]AstrodomeGridRing, len(contract)),
	}
	for index, ring := range contract {
		profile.Rings[index] = AstrodomeGridRing{
			ElevationDegrees:   ring.elevationDegrees,
			AzimuthCount:       ring.azimuthCount,
			AzimuthStepDegrees: 360 / float64(ring.azimuthCount),
		}
	}
	return profile
}

// Validate verifies the complete immutable ring contract, including order,
// exact elevation anchors, cardinality, and the single implicit zenith.
func (profile AstrodomeGridProfile) Validate() error {
	canonical, err := NewAstrodomeGridProfile(profile.ID)
	if err != nil {
		return err
	}
	if len(profile.Rings) != len(canonical.Rings) {
		return fmt.Errorf("astrodome grid %q has %d rings, want %d", profile.ID, len(profile.Rings), len(canonical.Rings))
	}
	for index, want := range canonical.Rings {
		got := profile.Rings[index]
		if got.ElevationDegrees != want.ElevationDegrees || got.AzimuthCount != want.AzimuthCount ||
			!finite(got.AzimuthStepDegrees) || math.Abs(got.AzimuthStepDegrees-want.AzimuthStepDegrees) > 1e-12 {
			return fmt.Errorf("astrodome grid %q ring %d is (%g degrees, %d, %g degrees), want (%g degrees, %d, %g degrees)",
				profile.ID, index, got.ElevationDegrees, got.AzimuthCount, got.AzimuthStepDegrees,
				want.ElevationDegrees, want.AzimuthCount, want.AzimuthStepDegrees)
		}
	}
	if got, want := profile.NodeCount(), astrodomeGridNodeCount(profile.ID); got != want {
		return fmt.Errorf("astrodome grid %q has %d nodes, want %d", profile.ID, got, want)
	}
	return nil
}

// NodeCount returns all tilted nodes plus the single zenith node.
func (profile AstrodomeGridProfile) NodeCount() int {
	count := 1
	for _, ring := range profile.Rings {
		count += ring.AzimuthCount
	}
	return count
}

// GeometryDescriptor returns the canonical, language-independent angular
// sampling and tessellation contract. It is intentionally distinct from
// AstrodomeGeometryVersion, which identifies the physical ICON-sphere ray.
func (profile AstrodomeGridProfile) GeometryDescriptor() (string, error) {
	if err := profile.Validate(); err != nil {
		return "", err
	}
	var descriptor strings.Builder
	_, _ = fmt.Fprintf(&descriptor, "%s\nprofile=%s\n", AstrodomeGridGeometryVersion, profile.ID)
	descriptor.WriteString("coordinate=ENU\n")
	descriptor.WriteString("azimuth=degrees-clockwise-from-north\n")
	descriptor.WriteString("outer-boundary-elevation=10\n")
	_, _ = fmt.Fprintf(&descriptor, "tessellation=%s\n", AstrodomeGridTessellationVersion)
	descriptor.WriteString("outer-boundary-segments=360\n")
	descriptor.WriteString("rings=")
	for index, ring := range profile.Rings {
		if index > 0 {
			descriptor.WriteByte(';')
		}
		descriptor.WriteString(strconv.FormatFloat(ring.ElevationDegrees, 'g', -1, 64))
		descriptor.WriteByte('/')
		descriptor.WriteString(strconv.Itoa(ring.AzimuthCount))
		descriptor.WriteByte('/')
		descriptor.WriteString(strconv.FormatFloat(ring.AzimuthStepDegrees, 'g', -1, 64))
	}
	descriptor.WriteString("\nzenith=90/null\n")
	descriptor.WriteString("node-order=rings-ascending-elevation/azimuth-ascending/zenith-last")
	return descriptor.String(), nil
}

// GeometryDigest binds a payload and frontend mesh to the exact canonical
// descriptor. The sha256 prefix prevents an unlabelled hash algorithm.
func (profile AstrodomeGridProfile) GeometryDigest() (string, error) {
	descriptor, err := profile.GeometryDescriptor()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(descriptor))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func astrodomeGridNodeCount(id AstrodomeGridProfileID) int {
	switch id {
	case AstrodomeGridDenseV1:
		return 353
	case AstrodomeGridSparseStorageV1:
		return 97
	case AstrodomeGridProductionV2:
		return 129
	default:
		return 0
	}
}

// Nodes returns ring-major nodes in deterministic order, followed by one
// zenith. For every tilted ring k is 0..N-1 and azimuth is exactly 360*k/N.
func (profile AstrodomeGridProfile) Nodes() ([]AstrodomeGridNode, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	nodes := make([]AstrodomeGridNode, 0, profile.NodeCount())
	for ringIndex, ring := range profile.Rings {
		for azimuthIndex := range ring.AzimuthCount {
			azimuth := 360 * float64(azimuthIndex) / float64(ring.AzimuthCount)
			nodes = append(nodes, AstrodomeGridNode{
				Index:            len(nodes),
				RingIndex:        ringIndex,
				AzimuthIndex:     azimuthIndex,
				ElevationDegrees: ring.ElevationDegrees,
				AzimuthDegrees:   &azimuth,
			})
		}
	}
	nodes = append(nodes, AstrodomeGridNode{
		Index:            len(nodes),
		RingIndex:        -1,
		AzimuthIndex:     -1,
		ElevationDegrees: AstrodomeZenithElevationDegrees,
		AzimuthDegrees:   nil,
	})
	return nodes, nil
}

// NewAstrodomeValidTimes creates the exact 72-frame hourly UTC axis beginning
// at firstValidAt. Input acquisition may use extra bracketing steps, but they
// are not part of this user-facing axis.
func NewAstrodomeValidTimes(firstValidAt time.Time) ([]time.Time, error) {
	if err := validateAstrodomeUTCWholeHour(firstValidAt); err != nil {
		return nil, fmt.Errorf("astrodome first valid time: %w", err)
	}
	validTimes := make([]time.Time, AstrodomeFrameCount)
	for index := range validTimes {
		validTimes[index] = firstValidAt.Add(time.Duration(index) * time.Hour)
	}
	return validTimes, nil
}

// ValidateAstrodomeValidTimes accepts one to 72 consecutive hourly frames and
// rejects non-UTC timestamps, sub-hour timestamps, duplicates, and gaps.
func ValidateAstrodomeValidTimes(validTimes []time.Time) error {
	if len(validTimes) == 0 || len(validTimes) > AstrodomeFrameCount {
		return fmt.Errorf("astrodome needs 1..%d valid times, got %d", AstrodomeFrameCount, len(validTimes))
	}
	for index, validAt := range validTimes {
		if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
			return fmt.Errorf("astrodome valid time %d: %w", index, err)
		}
		if index > 0 && !validAt.Equal(validTimes[index-1].Add(time.Hour)) {
			return fmt.Errorf("astrodome valid times %d and %d are not strictly one hour apart", index-1, index)
		}
	}
	return nil
}

func validateAstrodomeUTCWholeHour(value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	_, offsetSeconds := value.Zone()
	if offsetSeconds != 0 {
		return fmt.Errorf("timestamp must be UTC")
	}
	if value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
		return fmt.Errorf("timestamp must be aligned to a whole UTC hour")
	}
	return nil
}
