package render

import (
	"embed"
	"fmt"
	"image"
	"image/png"
	"sync"

	"bot_astrosferum/internal/astronomy"

	xdraw "golang.org/x/image/draw"
)

// The raster assets are presentation-only thumbnails. Horizontal coordinates,
// event times, and scientific indices never depend on the artwork.
//
//go:embed assets/celestial/*.png
var celestialAssetFS embed.FS

var (
	celestialAssetsOnce sync.Once
	celestialAssets     map[astronomy.CelestialBody]image.Image
	celestialAssetsErr  error
)

func celestialBodyImage(body astronomy.CelestialBody) (image.Image, error) {
	celestialAssetsOnce.Do(func() {
		celestialAssets = make(map[astronomy.CelestialBody]image.Image, len(astronomy.CelestialBodies()))
		for _, candidate := range astronomy.CelestialBodies() {
			path := fmt.Sprintf("assets/celestial/%s.png", candidate)
			file, err := celestialAssetFS.Open(path)
			if err != nil {
				celestialAssetsErr = fmt.Errorf("open celestial artwork %s: %w", candidate, err)
				return
			}
			decoded, err := png.Decode(file)
			_ = file.Close()
			if err != nil {
				celestialAssetsErr = fmt.Errorf("decode celestial artwork %s: %w", candidate, err)
				return
			}
			celestialAssets[candidate] = decoded
		}
	})
	if celestialAssetsErr != nil {
		return nil, celestialAssetsErr
	}
	asset, ok := celestialAssets[body]
	if !ok {
		return nil, fmt.Errorf("celestial artwork %q is unavailable", body)
	}
	return asset, nil
}

func drawCelestialBodyImage(canvas *image.RGBA, body astronomy.CelestialBody, centerX, centerY, size int) bool {
	asset, err := celestialBodyImage(body)
	if err != nil || size <= 0 {
		return false
	}
	half := size / 2
	destination := image.Rect(centerX-half, centerY-half, centerX-half+size, centerY-half+size)
	xdraw.CatmullRom.Scale(canvas, destination, asset, asset.Bounds(), xdraw.Over, nil)
	return true
}
