package main

import (
	"context"
	"fmt"
	"image"
	"image/draw"        // stdlib — склейка тайлов на общее полотно
	"image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw" // качественный ресайз
)

// соберём фон из XYZ-тайлов по bbox
// urlTpl: напр. "https://tiles.stadiamaps.com/tiles/alidade_smooth/{z}/{x}/{y}.png?api_key=KEY"
func fetchTilesComposite(ctx context.Context, urlTpl string, bb boundsLL, outW, outH int) (image.Image, error) {
	urlTpl = strings.TrimSpace(urlTpl)

	// 1) подобрать zoom
	z := chooseZoom(bb, outW)
	if z < 0 { z = 0 }
	if z > 20 { z = 20 }

	// 2) пиксельные координаты bbox в WebMercator при этом zoom
	minPx, minPy := lonLatToPixel(bb.minLon, bb.maxLat, z) // верхняя левая
	maxPx, maxPy := lonLatToPixel(bb.maxLon, bb.minLat, z) // нижняя правая

	// 3) диапазон тайлов 256x256
	const tile = 256
	minTx, minTy := minPx/tile, minPy/tile
	maxTx, maxTy := (maxPx-1)/tile, (maxPy-1)/tile

	// 4) холст
	wTiles := (maxTx - minTx + 1)
	hTiles := (maxTy - minTy + 1)
	bigW := wTiles * tile
	bigH := hTiles * tile
	big := image.NewRGBA(image.Rect(0, 0, bigW, bigH))

	// 5) качаем параллельно
	type job struct{ X, Y, Z int }
	jobs := make(chan job, wTiles*hTiles)
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	client := &http.Client{Timeout: 20 * time.Second}
	workers := 6
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select { case <-ctx.Done(): return; default: }
				u := strings.ReplaceAll(urlTpl, "{z}", fmt.Sprintf("%d", j.Z))
				u = strings.ReplaceAll(u, "{x}", fmt.Sprintf("%d", j.X))
				u = strings.ReplaceAll(u, "{y}", fmt.Sprintf("%d", j.Y))

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
				if err != nil { select { case errCh <- err: default: }; return }
				resp, err := client.Do(req)
				if err != nil { select { case errCh <- err: default: }; return }
				if resp.StatusCode/100 != 2 {
					b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
					resp.Body.Close()
					select {
					case errCh <- fmt.Errorf("tile %d/%d/%d HTTP %d: %s", j.Z, j.X, j.Y, resp.StatusCode, strings.TrimSpace(string(b))):
					default: }
					return
				}
				img, err := png.Decode(resp.Body)
				resp.Body.Close()
				if err != nil { select { case errCh <- err: default: }; return }

				offX := (j.X - minTx) * tile
				offY := (j.Y - minTy) * tile
				draw.Draw(big, image.Rect(offX, offY, offX+tile, offY+tile), img, image.Point{}, draw.Src)
			}
		}()
	}
	for ty := minTy; ty <= maxTy; ty++ {
		for tx := minTx; tx <= maxTx; tx++ {
			jobs <- job{X: tx, Y: ty, Z: z}
		}
	}
	close(jobs)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case <-done:
	}

	// 6) crop по bbox
	crop := image.Rect(minPx-minTx*tile, minPy-minTy*tile, maxPx-minTx*tile, maxPy-minTy*tile)
	if !crop.In(big.Bounds()) { crop = crop.Intersect(big.Bounds()) }
	cropped := big.SubImage(crop)

	// 7) масштаб до нужного размера
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), draw.Src, nil)
	return dst, nil
}

func chooseZoom(bb boundsLL, outW int) int {
	for z := 2; z <= 20; z++ {
		pxMin, _ := lonLatToPixel(bb.minLon, 0, z)
		pxMax, _ := lonLatToPixel(bb.maxLon, 0, z)
		if pxMax-pxMin >= outW { return z }
	}
	return 20
}

// WebMercator: lon/lat -> пиксели на зуме z
func lonLatToPixel(lon, lat float64, z int) (px, py int) {
	s := math.Ldexp(256, z) // 256 * 2^z
	mx := (lon + 180.0) / 360.0
	lat = clamp(lat, -85.05112878, 85.05112878)
	sin := math.Sin(lat * math.Pi / 180.0)
	my := 0.5 - math.Log((1+sin)/(1-sin))/(4*math.Pi)
	x := mx * s
	y := my * s
	return int(math.Floor(x + 0.5)), int(math.Floor(y + 0.5))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo { return lo }
	if v > hi { return hi }
	return v
}
