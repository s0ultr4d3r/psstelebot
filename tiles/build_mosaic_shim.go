package tiles

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
)

// BuildMosaic собирает фоновую мозаику тайлов, покрывающую bbox (в градусах),
// и масштабирует её в точный размер w×h. Возвращает RGBA и выбранный zoom.
// Fetcher оставлен в сигнатуре для совместимости, здесь он не используется.
func BuildMosaic(ctx context.Context, _ *Fetcher, preset Preset,
	minLon, minLat, maxLon, maxLat float64, w, h int,
) (*image.RGBA, int, error) {

	const tileSize = 256

	// выбрать максимальный зум, при котором bbox помещается в w×h (в пикселях тайлов этого зума)
	pickZoom := func() int {
		for z := preset.MaxZoom; z >= preset.MinZoom; z-- {
			x1 := MercX(minLon) * float64(tileSize) * math.Exp2(float64(z))
			x2 := MercX(maxLon) * float64(tileSize) * math.Exp2(float64(z))
			y1 := MercY(minLat) * float64(tileSize) * math.Exp2(float64(z))
			y2 := MercY(maxLat) * float64(tileSize) * math.Exp2(float64(z))
			if math.Abs(x2-x1) <= float64(w) && math.Abs(y2-y1) <= float64(h) {
				return z
			}
		}
		return preset.MinZoom
	}

	z := pickZoom()

	// worldRect в пикселях тайлов для выбранного зума
	x1 := MercX(minLon) * float64(tileSize) * math.Exp2(float64(z))
	x2 := MercX(maxLon) * float64(tileSize) * math.Exp2(float64(z))
	y1 := MercY(minLat) * float64(tileSize) * math.Exp2(float64(z))
	y2 := MercY(maxLat) * float64(tileSize) * math.Exp2(float64(z))
	world := image.Rect(int(math.Floor(x1)), int(math.Floor(y1)), int(math.Ceil(x2)), int(math.Ceil(y2)))

	// диапазон тайлов
	minTx := int(math.Floor(float64(world.Min.X) / float64(tileSize)))
	maxTx := int(math.Floor(float64(world.Max.X-1) / float64(tileSize)))
	minTy := int(math.Floor(float64(world.Min.Y) / float64(tileSize)))
	maxTy := int(math.Floor(float64(world.Max.Y-1) / float64(tileSize)))

	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// непрозрачный тёмный фон на случай пропадающих тайлов
	fill := color.NRGBA{R: 12, G: 12, B: 12, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = fill.R
			dst.Pix[i+1] = fill.G
			dst.Pix[i+2] = fill.B
			dst.Pix[i+3] = fill.A
		}
	}

	scale := math.Min(float64(w)/float64(world.Dx()), float64(h)/float64(world.Dy()))

	type tileJob struct {
		tx, ty int
		img    image.Image
	}

	ch := make(chan tileJob, (maxTx-minTx+1)*(maxTy-minTy+1))
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 20 * time.Second}

	for ty := minTy; ty <= maxTy; ty++ {
		for tx := minTx; tx <= maxTx; tx++ {
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				url, _ := preset.FillURL(z, tx, ty)

				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				for k, v := range preset.Headers {
					req.Header.Set(k, v)
				}
				resp, err := client.Do(req)
				if err != nil {
					return // тайл пропущен
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return
				}

				// Декодируем PNG или JPEG. Сначала по Content-Type, затем — авто-детект.
				var im image.Image
				ct := strings.ToLower(resp.Header.Get("Content-Type"))
				switch {
				case strings.Contains(ct, "png"):
					im, err = png.Decode(resp.Body)
				case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
					im, err = jpeg.Decode(resp.Body)
				default:
					// авто-детект (на всякий случай)
					// импорт конкретных декодеров выше обеспечивает регистрацию форматов
					im, _, err = image.Decode(resp.Body)
				}
				if err != nil || im == nil {
					return
				}

				ch <- tileJob{tx: tx, ty: ty, img: im}
			}(tx, ty)
		}
	}
	go func() { wg.Wait(); close(ch) }()

	for j := range ch {
		srcRect := image.Rect(j.tx*tileSize, j.ty*tileSize, (j.tx+1)*tileSize, (j.ty+1)*tileSize)
		inter := srcRect.Intersect(world)
		if inter.Empty() {
			continue
		}
		dr := image.Rect(
			int(math.Round(float64(inter.Min.X-world.Min.X)*scale)),
			int(math.Round(float64(inter.Min.Y-world.Min.Y)*scale)),
			int(math.Round(float64(inter.Max.X-world.Min.X)*scale)),
			int(math.Round(float64(inter.Max.Y-world.Min.Y)*scale)),
		)
		sr := image.Rect(inter.Min.X-srcRect.Min.X, inter.Min.Y-srcRect.Min.Y, inter.Max.X-srcRect.Min.X, inter.Max.Y-srcRect.Min.Y)
		draw.CatmullRom.Scale(dst, dr, j.img, sr, draw.Over, nil)
	}

	return dst, z, nil
}
