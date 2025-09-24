package tiles

import (
	"image"
	"math"
	"sync"

	"golang.org/x/image/draw"
)

// ComposeMosaic renders a tile mosaic for a given world rectangle (in "tile pixels"
// at zoom z) into a square canvas. Returns the canvas, origin of the world rect
// (in tile px), and scale factor (canvas/world).
func ComposeMosaic(tl *Loader, presetURL string, z int, worldRect image.Rectangle, canvas, tile int) (*image.RGBA, image.Point, float64, error) {
	scale := float64(canvas) / float64(max(worldRect.Dx(), worldRect.Dy()))
	dst := image.NewRGBA(image.Rect(0, 0, canvas, canvas))

	minTx := int(math.Floor(float64(worldRect.Min.X) / float64(tile)))
	maxTx := int(math.Floor(float64(worldRect.Max.X-1) / float64(tile)))
	minTy := int(math.Floor(float64(worldRect.Min.Y) / float64(tile)))
	maxTy := int(math.Floor(float64(worldRect.Max.Y-1) / float64(tile)))

	type job struct {
		tx, ty int
		im     image.Image
		err    error
	}
	ch := make(chan job, (maxTx-minTx+1)*(maxTy-minTy+1))
	var wg sync.WaitGroup

	for ty := minTy; ty <= maxTy; ty++ {
		for tx := minTx; tx <= maxTx; tx++ {
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				u := Sub(presetURL, z, tx, ty)
				im, err := tl.GetImage(u)
				ch <- job{tx: tx, ty: ty, im: im, err: err}
			}(tx, ty)
		}
	}
	go func() { wg.Wait(); close(ch) }()

	for j := range ch {
		if j.err != nil {
			return nil, image.Point{}, 1.0, j.err
		}
		srcRect := image.Rect(j.tx*tile, j.ty*tile, (j.tx+1)*tile, (j.ty+1)*tile)
		inter := srcRect.Intersect(worldRect)
		if inter.Empty() {
			continue
		}
		dr := image.Rect(
			int(math.Round(float64(inter.Min.X-worldRect.Min.X)*scale)),
			int(math.Round(float64(inter.Min.Y-worldRect.Min.Y)*scale)),
			int(math.Round(float64(inter.Max.X-worldRect.Min.X)*scale)),
			int(math.Round(float64(inter.Max.Y-worldRect.Min.Y)*scale)),
		)
		sr := image.Rect(inter.Min.X-srcRect.Min.X, inter.Min.Y-srcRect.Min.Y, inter.Max.X-srcRect.Min.X, inter.Max.Y-srcRect.Min.Y)
		draw.CatmullRom.Scale(dst, dr, j.im, sr, draw.Over, nil)
	}

	return dst, worldRect.Min, scale, nil
}

func max(a, b int) int {
	if a > b { return a }
	return b
}
