package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/s0ultr4d3r/psstelebot/tiles"
)

type multiIn []string

func (m *multiIn) String() string     { return strings.Join(*m, ",") }
func (m *multiIn) Set(s string) error { *m = append(*m, s); return nil }

var (
	inMany        multiIn
	outGIF        = flag.String("out", "synced.gif", "куда сохранить GIF")
	size          = flag.Int("size", 512, "размер кадра (квадрат)")
	fps           = flag.Float64("fps", 20.0, "кадров в секунду")
	duration      = flag.Duration("duration", 12*time.Second, "длительность итогового GIF (например, 12s)")
	margin        = flag.Float64("margin", 0.05, "поля вокруг bbox (доля после квадратирования, 0..0.25)")
	bgHex         = flag.String("bg", "#000000", "цвет фона (hex, если нет карты)")
	lineColorsStr = flag.String("lineColors", "#ffffff,#ff3b30,#34c759,#007aff,#ffcc00,#af52de", "список цветов линий для треков, через запятую (hex)")
	lineWidth     = flag.Int("lineWidth", 6, "толщина линии трека в пикселях")
	padPx         = flag.Int("padPx", -1, "доп. рамка вокруг треков в пикселях (после квадратирования и margin); -1 = авто (lineWidth+8)")
	pprofAddr     = flag.String("pprof", "", "включить pprof на адресе (например 127.0.0.1:6060), пусто = выключено")

	// HUD
	showSpeed    = flag.Bool("speedOverlay", true, "show current speed overlay (HUD)")
	speedUnits   = flag.String("speedUnits", "kmh", "speed units: kmh|ms|mph")
	speedSmoothN = flag.Int("speedSmooth", 5, "moving average window (points) for speed")
	showLegend   = flag.Bool("legend", true, "show legend: color — filename")

	// статичная картинка (Mapbox/MapTiler и др.)
	staticURL = flag.String("staticURL", "", "шаблон URL статической карты с плейсхолдателями {minLon},{minLat},{maxLon},{maxLat},{w},{h}")

	// тайловые карты через модуль tiles
	tilesPreset = flag.String("tilesPreset", "", "opentopomap | esri-satellite | maptiler-satellite | stamen-terrain-bg")
	tilesURL    = flag.String("tilesURL", "", "custom tile URL template with {z}/{x}/{y}")
	tileCache   = flag.String("tileCache", ".tile-cache", "tile cache dir")
	tilesRPS    = flag.Float64("tilesRPS", 1.0, "tile requests per second (OpenTopoMap≈1)")
	tilesBurst  = flag.Int("tilesBurst", 1, "tile burst")
	tilesTO     = flag.Duration("tilesTimeout", 8*time.Second, "tile HTTP timeout")

	// подгонка картинки только для staticURL
	tileFit = flag.String("tileFit", "contain", "fit mode for static map background: contain | cover")

	// ретраи/зумы
	tilesMaxZ        = flag.Int("tilesMaxZoom", -1, "override max tile zoom for the preset (-1 = preset default)")
	tilesMinZ        = flag.Int("tilesMinZoom", 10, "lower bound for auto downscale when 404 (inclusive)")
	tilesRetries     = flag.Int("tilesRetries", 3, "retry attempts for non-404 tile errors")
	tilesRetryBackoff = flag.Duration("tilesRetryBackoff", 2*time.Second, "initial backoff for tile retries (exponential)")

	timeout = flag.Duration("timeout", 10*time.Minute, "жёсткий таймаут всего процесса")
)

// types.go (минимум, чтобы собрать скорость из GPX с таймами)
type GPXPt struct {
	Lat, Lon float64
	Ele      float64
	Time     time.Time
	SX, SY   float64
}

type Track struct {
	Name   string
	Color  color.Color
	Points []GPXPt
	Speeds []float64 // м/с, со сглаживанием
	T0, T1 time.Time // min/max time (для синхронизации по времени)
}

func main() {
	flag.Var(&inMany, "in", "путь к GPX (можно указывать много раз)")
	flag.Parse()

	if *pprofAddr != "" {
		enablePPROF(*pprofAddr)
	}

	if len(inMany) == 0 {
		inMany = append(inMany, "track.gpx")
	}

	ctx, cancel := withTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx, inMany, *outGIF, *size, *fps, *duration, *margin, *bgHex, *lineColorsStr, *staticURL, *tilesURL); err != nil {
		log.Fatalf("❌ Ошибка: %v", err)
	}
	log.Printf("✅ Готово: %s", *outGIF)
}

func run(
	ctx context.Context,
	inPaths []string,
	outPath string,
	px int,
	fps float64,
	dur time.Duration,
	margin float64,
	bgHex string,
	lineColorsCSV string,
	staticURLArg string,
	tilesURLArg string,
) error {
	if fps <= 0 {
		return errors.New("fps должен быть > 0")
	}
	if px < 64 || px > 4096 {
		return fmt.Errorf("неподходящий размер: %d (должен быть 64..4096)", px)
	}
	if margin < 0 || margin >= 0.25 {
		return fmt.Errorf("margin должен быть в диапазоне [0..0.25), сейчас: %.3f", margin)
	}

	// загрузка GPX (для геометрии/анимации)
	var tracks [][]PtLL
	totalPts := 0
	for _, p := range inPaths {
		pts, err := ParseGPXFile(p)
		if err != nil {
			return fmt.Errorf("parse gpx %s: %w", p, err)
		}
		if len(pts) == 0 {
			continue
		}
		tracks = append(tracks, pts)
		totalPts += len(pts)
	}
	if len(tracks) == 0 {
		return errors.New("нет точек во входных GPX")
	}

	// прогресс-бары
	bars := NewBars(totalPts, 0)
	defer bars.Done()
	for ti := range tracks {
		for range tracks[ti] {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			bars.IncGPX()
		}
	}

	totalFrames := int(math.Max(1, fps*dur.Seconds()))

	bg, err := ParseHexColor(bgHex)
	if err != nil {
		return fmt.Errorf("bg color: %w", err)
	}

	trackColors, err := ParseHexColors(lineColorsCSV)
	if err != nil {
		return fmt.Errorf("lineColors: %w", err)
	}
	if len(trackColors) == 0 {
		return errors.New("lineColors пуст — укажите хотя бы один цвет")
	}

	// ---------- ЕДИНЫЙ VIEWPORT В WEB MERCATOR ----------
	// общий bbox по трекам (в градусах)
	bb := boundsLL{minLat: math.MaxFloat64, minLon: math.MaxFloat64, maxLat: -math.MaxFloat64, maxLon: -math.MaxFloat64}
	for _, pts := range tracks {
		b := bboxLL(pts)
		if b.minLat < bb.minLat {
			bb.minLat = b.minLat
		}
		if b.minLon < bb.minLon {
			bb.minLon = b.minLon
		}
		if b.maxLat > bb.maxLat {
			bb.maxLat = b.maxLat
		}
		if b.maxLon > bb.maxLon {
			bb.maxLon = b.maxLon
		}
	}

	// 1) градусы -> меркатор (нормализованный [0..1])
	x0 := mercX(bb.minLon)
	x1 := mercX(bb.maxLon)
	yTop := mercY(bb.maxLat) // верх (численно меньше)
	yBot := mercY(bb.minLat) // низ  (численно больше)

	// 2) делаем квадрат "contain-квадратом": РАСШИРЯЕМ меньшую сторону (ничего не режем!)
	dx := x1 - x0
	dy := yBot - yTop
	if dx <= 0 || dy <= 0 {
		return fmt.Errorf("degenerate bbox")
	}
	a := dx / dy
	if a > 1.0 {
		add := (dx - dy) / 2
		yTop -= add
		yBot += add
	} else if a < 1.0 {
		add := (dy - dx) / 2
		x0 -= add
		x1 += add
	}

	// 3) добавляем margin
	dx = x1 - x0
	dy = yBot - yTop
	x0 -= dx * margin
	x1 += dx * margin
	yTop -= dy * margin
	yBot += dy * margin

	// 4) добавляем доп. рамку в px
	autoPad := float64(*lineWidth) + 8.0
	pad := autoPad
	if *padPx >= 0 {
		pad = float64(*padPx)
	}
	expandX := pad / float64(px) * (x1 - x0)
	expandY := pad / float64(px) * (yBot - yTop)
	x0 -= expandX
	x1 += expandX
	yTop -= expandY
	yBot += expandY

	// итоговый viewport в градусах — для тайлов и для проекции треков
	bbLLFit := boundsLL{
		minLon: x0*360 - 180,
		maxLon: x1*360 - 180,
		maxLat: invMercY(yTop), // top
		minLat: invMercY(yBot), // bottom
	}

	// ---------- ФОН ----------
	var baseImg image.Image

	switch {
	case staticURLArg != "":
		// Заглушка для статичных карт — в этой сборке ты используешь тайлы
		url := expandStaticURL(staticURLArg, bbLLFit, px, px)
		img, ferr := fetchStaticMap(ctx, url)
		if ferr != nil {
			return fmt.Errorf("fetch map: %w", ferr)
		}
		baseImg = fitBaseToCanvas(img, px, px, *tileFit, bg)

	case *tilesPreset != "" || tilesURLArg != "":
		fetcher, ferr := tiles.NewFetcher(*tileCache, *tilesRPS, *tilesBurst, *tilesTO)
		if ferr != nil {
			return fmt.Errorf("tiles fetcher: %w", ferr)
		}

		var preset tiles.Preset
		if *tilesPreset != "" {
			if *tilesPreset == "stamen-terrain-bg" {
				// Переопределяем на оригинальный Stamen без ключа
				preset = tiles.Preset{
					Name:        "stamen-terrain-bg",
					URLTmpl:     "https://tile.stamen.com/terrain-background/{z}/{x}/{y}.jpg",
					Attribution: "Map tiles by Stamen Design (CC BY 3.0) — Data © OpenStreetMap contributors (ODbL)",
					MinZoom:     0, MaxZoom: 18,
				}
			} else {
				p, ok := tiles.Presets[*tilesPreset]
				if !ok {
					return fmt.Errorf("unknown tilesPreset: %s", *tilesPreset)
				}
				preset = p
			}
		} else {
			preset = tiles.Preset{
				Name:        "custom",
				URLTmpl:     tilesURLArg,
				Attribution: "© data providers",
				MinZoom:     0, MaxZoom: 22,
			}
		}

		// Применяем override по макс. зуму, если задан
		if *tilesMaxZ >= 0 {
			if *tilesMaxZ < preset.MinZoom {
				preset.MinZoom = *tilesMaxZ
			}
			preset.MaxZoom = *tilesMaxZ
		}

		bgRGBA, usedZoom, merr := buildMosaicWithRetriesAndDownscale(
			ctx, fetcher, preset,
			bbLLFit.minLon, bbLLFit.minLat, bbLLFit.maxLon, bbLLFit.maxLat,
			px, px,
			*tilesRetries, *tilesRetryBackoff, *tilesMinZ,
		)
		if merr != nil {
			return fmt.Errorf("build mosaic: %w", merr)
		}
		log.Printf("[tiles] mosaic done at z=%d (%dx%d)", usedZoom, px, px)

		// гарантируем точный размер px×px
		base := bgRGBA
		if base.Bounds().Dx() != px || base.Bounds().Dy() != px {
			scaled := image.NewRGBA(image.Rect(0, 0, px, px))
			xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), base, base.Bounds(), xdraw.Over, nil)
			base = scaled
		}
		baseImg = base

		// атрибуция поверх RGBA
		if dst, ok := baseImg.(*image.RGBA); ok {
			tiles.DrawAttribution(dst, preset.Attribution)
		}
	}

	// ---------- КАДРЫ ОТРИСОВКИ ТРЕКОВ ----------
	frames, delays, err := BuildFramesMulti(
		ctx, tracks, px, totalFrames, margin,
		bg, trackColors, *lineWidth, baseImg,
		bbLLFit, // единый viewport
	)
	if err != nil {
		return fmt.Errorf("build frames: %w", err)
	}
	bars.GIF.ChangeMax(len(frames))

	// ---------- HUD: данные (имена, цвета, скорости) ----------
	hudTracks := make([]hudTrack, 0, len(tracks))
	for i, pts := range tracks {
		name := filepath.Base(inPaths[i])
		col := trackColors[i%len(trackColors)]
		sp := computeSpeedsPtLL(pts, *speedSmoothN) // м/с
		hudTracks = append(hudTracks, hudTrack{
			Name:   name,
			Color:  col,
			Speeds: sp,
			Pts:    pts,
		})
	}

	// глобальная шкала времени для HUD
	var hasTime bool
	var t0, t1 time.Time
	for _, tr := range hudTracks {
		for _, p := range tr.Pts {
			if p.T == nil {
				continue
			}
			if !hasTime {
				t0, t1, hasTime = *p.T, *p.T, true
			} else {
				if p.T.Before(t0) {
					t0 = *p.T
				}
				if p.T.After(t1) {
					t1 = *p.T
				}
			}
		}
	}

	// ---------- дорисовываем HUD поверх каждого кадра ----------
	if *showLegend || *showSpeed {
		for i := range frames {
			dst := frames[i].Img // *image.Paletted (implements draw.Image)
			if *showLegend {
				hudDrawLegend(dst, hudTracks)
			}
			if *showSpeed && hasTime && t1.After(t0) {
				var tNow time.Time
				if i == len(frames)-1 {
					tNow = t1
				} else {
					f := float64(i) / float64(len(frames)-1)
					tNow = t0.Add(time.Duration(f * float64(t1.Sub(t0))))
				}
				hudDrawSpeeds(dst, hudTracks, tNow, *speedUnits)
			}
		}
	}

	// ---------- запись GIF ----------
	tmpOut := outPath + ".part"
	if err := encodeGIF(ctx, frames, delays, tmpOut, func(i int) { bars.SetGIF(i + 1) }); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("encode gif: %w", err)
	}
	if err := os.Rename(tmpOut, outPath); err != nil {
		if err := copyFile(tmpOut, outPath); err != nil {
			return fmt.Errorf("rename/copy gif: %w", err)
		}
		_ = os.Remove(tmpOut)
	}
	return nil
}

// --- мозайка с ретраями и автодаунскейлом зума ---
func buildMosaicWithRetriesAndDownscale(
	ctx context.Context,
	fetcher *tiles.Fetcher,
	preset tiles.Preset,
	minLon, minLat, maxLon, maxLat float64,
	w, h int,
	retries int,
	backoff time.Duration,
	minZoomBound int,
) (*image.RGBA, int, error) {

	// начнём с текущего MaxZoom пресета
	startMax := preset.MaxZoom
	if startMax <= 0 {
		startMax = 18
	}
	if minZoomBound <= 0 {
		minZoomBound = 10
	}
	curMax := startMax

	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		// на каждом заходе пробуем собрать мозаику с текущим cap на MaxZoom
		curPreset := preset
		curPreset.MaxZoom = curMax
		if curPreset.MinZoom > curPreset.MaxZoom {
			curPreset.MinZoom = curPreset.MaxZoom
		}

		img, usedZ, err := tiles.BuildMosaic(ctx, fetcher, curPreset,
			minLon, minLat, maxLon, maxLat, w, h)

		if err == nil {
			return img, usedZ, nil
		}

		msg := err.Error()
		// Если 404 — уменьшаем MaxZoom и пробуем снова (без штрафа retry-счётчика)
		if strings.Contains(msg, " 404 ") || strings.Contains(strings.ToLower(msg), "404") {
			if curMax <= minZoomBound {
				return nil, 0, fmt.Errorf("tile 404 persists at z<=%d: %w", curMax, err)
			}
			curMax--
			log.Printf("[tiles] got 404 at z<=%d → downscale to MaxZoom=%d and retry", usedZ, curMax)
			continue
		}

		// Прочие ошибки — ретраи с экспоненциальным бэкоффом
		if retries > 0 {
			log.Printf("[tiles] mosaic err (%v), retry in %s (attempts left: %d)", err, backoff, retries)
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
			}
			retries--
			if backoff < 30*time.Second {
				backoff = time.Duration(float64(backoff) * 1.7)
			}
			continue
		}
		return nil, 0, err
	}
}

// --- encodeGIF: обёртка над writeGIFAll с прогресс-коллбеком ---
func encodeGIF(ctx context.Context, frames []*PalFrame, delays []int, outPath string, onFrame func(i int)) error {
	if len(frames) == 0 {
		return errors.New("нет кадров")
	}
	if len(delays) != len(frames) {
		return errors.New("len(delays) != len(frames)")
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for i := range frames {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if onFrame != nil {
			onFrame(i)
		}
	}
	return writeGIFAll(f, frames, delays)
}

// --- copyFile: fallback, если os.Rename не сработал (разные FS и т.п.)
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Sync()
}

// ---- helper: подгонка статической карты под квадратный кадр ----
func fitBaseToCanvas(src image.Image, W, H int, mode string, bg color.Color) image.Image {
	if src == nil {
		return nil
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		dst := image.NewRGBA(image.Rect(0, 0, W, H))
		fillRGBA(dst, bg)
		return dst
	}
	if sw == W && sh == H {
		return src
	}

	sx := float64(W) / float64(sw)
	sy := float64(H) / float64(sh)
	scale := sx
	switch mode {
	case "contain":
		if sy < sx {
			scale = sy
		}
	case "cover":
		if sy > sx {
			scale = sy
		}
	default:
		if sy < sx {
			scale = sy
		}
	}

	tw := int(math.Ceil(float64(sw) * scale))
	th := int(math.Ceil(float64(sh) * scale))

	tmp := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.ApproxBiLinear.Scale(tmp, tmp.Bounds(), src, sb, xdraw.Over, nil)

	dst := image.NewRGBA(image.Rect(0, 0, W, H))
	fillRGBA(dst, bg)

	offX := (W - tw) / 2
	offY := (H - th) / 2

	db := dst.Bounds()
	sb2 := tmp.Bounds().Add(image.Pt(offX, offY))
	clip := db.Intersect(sb2)
	if clip.Empty() {
		return dst
	}
	srcPt := clip.Min.Sub(sb2.Min)
	xdraw.Copy(dst, clip.Min, tmp, image.Rect(srcPt.X, srcPt.Y, srcPt.X+clip.Dx(), srcPt.Y+clip.Dy()), xdraw.Over, nil)
	return dst
}

func fillRGBA(dst *image.RGBA, c color.Color) {
	b := dst.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, c)
		}
	}
}

/*** ======= скорость по точкам PtLL (используются Lat/Lon/T) ======= ***/

const earthR = 6371000.0 // meters

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	toRad := math.Pi / 180
	φ1, λ1 := lat1*toRad, lon1*toRad
	φ2, λ2 := lat2*toRad, lon2*toRad
	dφ := φ2 - φ1
	dλ := λ2 - λ1
	a := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthR * c
}

func computeSpeedsPtLL(pts []PtLL, smoothN int) []float64 {
	n := len(pts)
	if n == 0 {
		return nil
	}
	raw := make([]float64, n)
	raw[0] = 0
	for i := 1; i < n; i++ {
		if pts[i].T == nil || pts[i-1].T == nil {
			raw[i] = raw[i-1]
			continue
		}
		dt := pts[i].T.Sub(*pts[i-1].T).Seconds()
		if dt <= 0 {
			raw[i] = raw[i-1]
			continue
		}
		d := haversine(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
		v := d / dt // м/с
		// отбрасываем явные GPS-артефакты
		if v > 30.0 { // для пешком/вело; если авто — подними порог
			v = raw[i-1]
		}
		raw[i] = v
	}
	if smoothN <= 1 {
		return raw
	}
	out := make([]float64, n)
	sum := 0.0
	q := make([]float64, 0, smoothN)
	for i := 0; i < n; i++ {
		q = append(q, raw[i])
		sum += raw[i]
		if len(q) > smoothN {
			sum -= q[0]
			q = q[1:]
		}
		out[i] = sum / float64(len(q))
	}
	return out
}

func convertSpeed(vMS float64, units string) float64 {
	switch units {
	case "kmh":
		return vMS * 3.6
	case "mph":
		return vMS * 2.23693629
	default: // "ms"
		return vMS
	}
}
