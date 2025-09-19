package tiles

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// BuildMosaic fetches all tiles covering bbox and assembles into an RGBA image.
// It returns the mosaic and the actual zoom used.
func BuildMosaic(
	ctx context.Context,
	f *Fetcher,
	preset Preset,
	minLon, minLat, maxLon, maxLat float64,
	targetW, targetH int,
) (*image.RGBA, int, error) {

	// pick zoom to fit into target size
	z := FitZoom(minLon, minLat, maxLon, maxLat, targetW, targetH, preset)
	z = ClampZoom(z, preset)

	// world-pixel bbox at chosen zoom
	tlx, tly, brx, bry := BBoxPixels(minLon, minLat, maxLon, maxLat, z)
	w := int(math.Ceil(brx - tlx))
	h := int(math.Ceil(bry - tly))
	if w <= 0 || h <= 0 {
		return nil, z, fmt.Errorf("invalid mosaic size %dx%d", w, h)
	}
	if w > targetW || h > targetH {
		// safety (shouldn't happen with FitZoom)
		w, h = targetW, targetH
	}

	out := image.NewRGBA(image.Rect(0, 0, w, h))

	// range of tiles to fetch
	minTX, minTY, maxTX, maxTY := CoveringTiles(minLon, minLat, maxLon, maxLat, z)

	for ty := minTY; ty <= maxTY; ty++ {
		for tx := minTX; tx <= maxTX; tx++ {
			u, hdrs, err := f.URLFor(preset, z, tx, ty)
			if err != nil {
				return nil, z, err
			}
			data, _, err := f.GetTile(ctx, u, hdrs)
			if err != nil {
				return nil, z, fmt.Errorf("get tile %s: %w", u, err)
			}

			img, _, err := decodeTile(data)
			if err != nil {
				return nil, z, fmt.Errorf("decode tile %s: %w", u, err)
			}

			// where to paste this tile in mosaic?
			// compute top-left world-pixel of tile
			tilePx := float64(tx * TileSize)
			tilePy := float64(ty * TileSize)

			// offset in mosaic:
			offX := int(tilePx - tlx)
			offY := int(tilePy - tly)

			Paste(out, img, offX, offY)
		}
	}
	return out, z, nil
}

func decodeTile(b []byte) (image.Image, string, error) {
	// Fast path: check first bytes for PNG/JPEG
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		img, err := png.Decode(bytes.NewReader(b))
		return img, "image/png", err
	}
	if len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF {
		img, err := jpeg.Decode(bytes.NewReader(b))
		return img, "image/jpeg", err
	}
	// fallback to image.Decode (slower, but robust)
	img, format, err := image.Decode(bytes.NewReader(b))
	return img, format, err
}
