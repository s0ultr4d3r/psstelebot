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
	margin        = flag.Float64("margin", 0.05, "поля от краёв bbox (0..0.25)")
	bgHex         = flag.String("bg", "#000000", "цвет фона (hex, если нет карты)")
	lineColorsStr = flag.String("lineColors", "#ffffff,#ff3b30,#34c759,#007aff,#ffcc00,#af52de", "список цветов линий для треков, через запятую (hex)")
	lineWidth     = flag.Int("lineWidth", 4, "толщина линии трека в пикселях")
	pprofAddr     = flag.String("pprof", "", "включить pprof на адресе (например 127.0.0.1:6060), пусто = выключено")

	// статичная картинка (Mapbox/MapTiler и др.)
	staticURL = flag.String("staticURL", "", "шаблон URL статической карты с плейсхолдерами {minLon},{minLat},{maxLon},{maxLat},{w},{h}")

	// тайловые карты через модуль tiles
	tilesPreset = flag.String("tilesPreset", "", "opentopomap | esri-satellite | maptiler-satellite | stamen-terrain-bg")
	tilesURL    = flag.String("tilesURL", "", "custom tile URL template with {z}/{x}/{y}")
	tileCache   = flag.String("tileCache", ".tile-cache", "tile cache dir")
	tilesRPS    = flag.Float64("tilesRPS", 1.0, "tile requests per second (OpenTopoMap≈1)")
	tilesBurst  = flag.Int("tilesBurst", 1, "tile burst")
	tilesTO     = flag.Duration("tilesTimeout", 8*time.Second, "tile HTTP timeout")

	// подгонка карты под квадратный кадр
	tileFit = flag.String("tileFit", "contain", "fit mode for tile background: contain | cover")

	timeout = flag.Duration("timeout", 10*time.Minute, "жёсткий таймаут всего процесса")
)

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

	// загрузка GPX
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

	// общий bbox
	bb := boundsLL{minLat: math.MaxFloat64, minLon: math.MaxFloat64, maxLat: -math.MaxFloat64, maxLon: -math.MaxFloat64}
	for _, pts := range tracks {
		b := bboxLL(pts)
		if b.minLat < bb.minLat { bb.minLat = b.minLat }
		if b.minLon < bb.minLon { bb.minLon = b.minLon }
		if b.maxLat > bb.maxLat { bb.maxLat = b.maxLat }
		if b.maxLon > bb.maxLon { bb.maxLon = b.maxLon }
	}

	// паддинг по margin
	padLon := (bb.maxLon - bb.minLon) * margin
	padLat := (bb.maxLat - bb.minLat) * margin
	minLon := bb.minLon - padLon
	maxLon := bb.maxLon + padLon
	minLat := bb.minLat - padLat
	maxLat := bb.maxLat + padLat

	// фон
	var baseImg image.Image

	switch {
	case staticURLArg != "":
		url := expandStaticURL(staticURLArg, boundsLL{
			minLon: minLon, minLat: minLat,
			maxLon: maxLon, maxLat: maxLat,
		}, px, px)
		baseImg, err = fetchStaticMap(ctx, url)
		if err != nil {
			return fmt.Errorf("fetch map: %w", err)
		}
		baseImg = fitBaseToCanvas(baseImg, px, px, *tileFit, bg)

	case *tilesPreset != "" || tilesURLArg != "":
		fetcher, ferr := tiles.NewFetcher(*tileCache, *tilesRPS, *tilesBurst, *tilesTO)
		if ferr != nil {
			return fmt.Errorf("tiles fetcher: %w", ferr)
		}

		var preset tiles.Preset
		if *tilesPreset != "" {
			p, ok := tiles.Presets[*tilesPreset]
			if !ok {
				return fmt.Errorf("unknown tilesPreset: %s", *tilesPreset)
			}
			preset = p
		} else {
			preset = tiles.Preset{
				Name:        "custom",
				URLTmpl:     tilesURLArg,
				Attribution: "© data providers",
				MinZoom:     0,
				MaxZoom:     22,
			}
		}

		bgRGBA, _, merr := tiles.BuildMosaic(
			ctx, fetcher, preset,
			minLon, minLat, maxLon, maxLat,
			px, px,
		)
		if merr != nil {
			return fmt.Errorf("build mosaic: %w", merr)
		}
		tiles.DrawAttribution(bgRGBA, preset.Attribution)
		baseImg = fitBaseToCanvas(bgRGBA, px, px, *tileFit, bg)
	}

	// кадры
	frames, delays, err := BuildFramesMulti(
		ctx, tracks, px, totalFrames, margin,
		bg, trackColors, *lineWidth, baseImg,
	)
	if err != nil {
		return fmt.Errorf("build frames: %w", err)
	}
	bars.GIF.ChangeMax(len(frames))

	// запись
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
		onFrame(i)
	}
	return writeGIFAll(f, frames, delays)
}

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

// ---- helper: подгонка карты под квадратный кадр ----

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
	if mode == "contain" {
		if sy < sx { scale = sy }
	} else if mode == "cover" {
		if sy > sx { scale = sy }
	} else {
		if sy < sx { scale = sy }
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
