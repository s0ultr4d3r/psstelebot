package tiles

import (
	"image"
	"math"
)

const TileSize = 256

// mercX/Y: lon/lat (deg) -> normalized mercator [0..1]
func mercX(lon float64) float64 { return (lon + 180.0) / 360.0 }
func mercY(lat float64) float64 {
	lat = math.Min(85.05112878, math.Max(-85.05112878, lat))
	rad := lat * math.Pi / 180.0
	s := math.Sin(rad)
	y := 0.5 - math.Log((1+s)/(1-s))/(4*math.Pi)
	return y
}

// At zoom z, world size in pixels:
func worldSize(z int) float64 { return float64(TileSize) * math.Exp2(float64(z)) }

// LonLatToPixel returns pixel coords in "world pixels" at zoom z.
func LonLatToPixel(lon, lat float64, z int) (px, py float64) {
	ws := worldSize(z)
	px = mercX(lon) * ws
	py = mercY(lat) * ws
	return
}

// PixelToTile returns tile indices and pixel offset inside tile.
func PixelToTile(px, py float64) (tx, ty int, ox, oy int) {
	tx = int(math.Floor(px / TileSize))
	ty = int(math.Floor(py / TileSize))
	ox = int(px) - tx*TileSize
	oy = int(py) - ty*TileSize
	return
}

// BBoxPixels returns top-left & bottom-right world-pixel coords for given bbox & zoom.
func BBoxPixels(minLon, minLat, maxLon, maxLat float64, z int) (tlx, tly, brx, bry float64) {
	// top-left uses maxLat; bottom-right uses minLat
	tlx, tly = LonLatToPixel(minLon, maxLat, z)
	brx, bry = LonLatToPixel(maxLon, minLat, z)
	return
}

// CoveringTiles returns the inclusive tile range covering the bbox at zoom.
func CoveringTiles(minLon, minLat, maxLon, maxLat float64, z int) (minTX, minTY, maxTX, maxTY int) {
	tlx, tly, brx, bry := BBoxPixels(minLon, minLat, maxLon, maxLat, z)
	minTX = int(math.Floor(tlx / TileSize))
	minTY = int(math.Floor(tly / TileSize))
	maxTX = int(math.Floor((brx - 1) / TileSize))
	maxTY = int(math.Floor((bry - 1) / TileSize))
	return
}

// FitZoom tries to find a zoom that makes bbox fit into target WxH pixels.
func FitZoom(minLon, minLat, maxLon, maxLat float64, targetW, targetH int, preset Preset) int {
	// naive search from high to low
	for z := preset.MaxZoom; z >= preset.MinZoom; z-- {
		tlx, tly, brx, bry := BBoxPixels(minLon, minLat, maxLon, maxLat, z)
		w := brx - tlx
		h := bry - tly
		if int(math.Ceil(w)) <= targetW && int(math.Ceil(h)) <= targetH {
			return z
		}
	}
	return preset.MinZoom
}

// Paste pastes src into dst at (x,y), clipping to bounds.
func Paste(dst *image.RGBA, src image.Image, x, y int) {
	db := dst.Bounds()
	for sy := 0; sy < src.Bounds().Dy(); sy++ {
		dy := y + sy
		if dy < db.Min.Y || dy >= db.Max.Y {
			continue
		}
		for sx := 0; sx < src.Bounds().Dx(); sx++ {
			dx := x + sx
			if dx < db.Min.X || dx >= db.Max.X {
				continue
			}
			dst.Set(dx, dy, src.At(src.Bounds().Min.X+sx, src.Bounds().Min.Y+sy))
		}
	}
}
