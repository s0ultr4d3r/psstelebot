package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type multiIn []string

func (m *multiIn) String() string        { return strings.Join(*m, ",") }
func (m *multiIn) Set(s string) error    { *m = append(*m, s); return nil }

var (
	inMany    multiIn
	outGIF    = flag.String("out", "synced.gif", "куда сохранить GIF")
	size      = flag.Int("size", 512, "размер кадра (квадрат)")
	fps       = flag.Float64("fps", 20.0, "кадров в секунду")
	duration  = flag.Duration("duration", 12*time.Second, "длительность итогового GIF (например, 12s)")
	margin    = flag.Float64("margin", 0.05, "поля от краёв bbox (0..0.25)")
	bgHex     = flag.String("bg", "#000000", "цвет фона (hex, если нет карты)")
	lineColorsStr = flag.String("lineColors", "#ffffff,#ff3b30,#34c759,#007aff,#ffcc00,#af52de", "список цветов линий для треков, через запятую (hex)")
	pprofAddr = flag.String("pprof", "", "включить pprof на адресе (например 127.0.0.1:6060), пусто = выключено")

	// статичная картинка (Mapbox/MapTiler и др.)
	staticURL = flag.String("staticURL", "", "шаблон URL статической карты с плейсхолдерами {minLon},{minLat},{maxLon},{maxLat},{w},{h}")
	// тайловая схема (Stadia/XYZ)
	tilesURL  = flag.String("tilesURL", "", "шаблон тайлов, например: https://tiles.stadiamaps.com/tiles/alidade_smooth/{z}/{x}/{y}.png?api_key=KEY")

	timeout   = flag.Duration("timeout", 10*time.Minute, "жёсткий таймаут всего процесса")
)

func main() {
	flag.Var(&inMany, "in", "путь к GPX (можно указывать много раз)")
	flag.Parse()

	if *pprofAddr != "" {
		enablePPROF(*pprofAddr)
	}

	if len(inMany) == 0 {
		// обратная совместимость — если не указали, берём track.gpx
		inMany = append(inMany, "track.gpx")
	}

	ctx, cancel := withTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx, inMany, *outGIF, *size, *fps, *duration, *margin, *bgHex, *lineColorsStr, *staticURL, *tilesURL); err != nil {
		log.Fatalf("❌ Ошибка: %v", err)
	}
	log.Printf("✅ Готово: %s", *outGIF)
}

func run(ctx context.Context, inPaths []string, outPath string, px int, fps float64, dur time.Duration, margin float64, bgHex string, lineColorsCSV string, staticURL, tilesURL string) error {
	if fps <= 0 {
		return errors.New("fps должен быть > 0")
	}
	if px < 64 || px > 4096 {
		return fmt.Errorf("неподходящий размер: %d (должен быть 64..4096)", px)
	}
	if margin < 0 || margin >= 0.25 {
		return fmt.Errorf("margin должен быть в диапазоне [0..0.25), сейчас: %.3f", margin)
	}

	// грузим все GPX
	var tracks [][]PtLL
	totalPts := 0
	for _, p := range inPaths {
		pts, err := ParseGPXFile(p)
		if err != nil { return fmt.Errorf("parse gpx %s: %w", p, err) }
		if len(pts) == 0 { continue }
		tracks = append(tracks, pts)
		totalPts += len(pts)
	}
	if len(tracks) == 0 {
		return errors.New("нет точек во входных GPX")
	}

	// прогресс-бары
	bars := NewBars(totalPts, 0)
	defer bars.Done()

	// можно делать фильтрацию/децимацию в этом цикле
	for ti := range tracks {
		for range tracks[ti] {
			select { case <-ctx.Done(): return ctx.Err(); default: }
			bars.IncGPX()
		}
	}

	totalFrames := int(math.Max(1, fps*dur.Seconds()))

	bg, err := ParseHexColor(bgHex)
	if err != nil { return fmt.Errorf("bg color: %w", err) }

	trackColors, err := ParseHexColors(lineColorsCSV)
	if err != nil { return fmt.Errorf("lineColors: %w", err) }
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

	// фон: карта или однотон
	var baseImg image.Image
	switch {
	case staticURL != "":
		url := expandStaticURL(staticURL, bb, px, px)
		baseImg, err = fetchStaticMap(ctx, url)
		if err != nil { return fmt.Errorf("fetch map: %w", err) }
	case tilesURL != "":
		baseImg, err = fetchTilesComposite(ctx, tilesURL, bb, px, px)
		if err != nil { return fmt.Errorf("fetch tiles: %w", err) }
	}

	// кадры
	frames, delays, err := BuildFramesMulti(ctx, tracks, px, totalFrames, margin, bg, trackColors, baseImg)
	if err != nil { return fmt.Errorf("build frames: %w", err) }
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
	if len(frames) == 0 { return errors.New("нет кадров") }
	if len(delays) != len(frames) { return errors.New("len(delays) != len(frames)") }

	f, err := os.Create(outPath)
	if err != nil { return err }
	defer f.Close()

	for i := range frames {
		select { case <-ctx.Done(): return ctx.Err(); default: }
		onFrame(i)
	}
	return writeGIFAll(f, frames, delays)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { return err }
	in, err := os.Open(src)
	if err != nil { return err }
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil { return err }
	defer func() { _ = out.Close() }()
	if _, err := out.ReadFrom(in); err != nil { return err }
	return out.Sync()
}
