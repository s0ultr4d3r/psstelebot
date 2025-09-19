package main

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/color/palette"
	"math"
	"time"
)

// один палетизированный кадр
type PalFrame struct {
	Img   *image.Paletted
	Delay int // hundredths of a second
}

// мульти-рендер: несколько треков, разные цвета
// СИНХРОНИЗАЦИЯ ПО ВРЕМЕНИ: кадры равномерно покрывают интервал [minT..maxT].
// Для треков без времени есть фоллбэк по индексу.
func BuildFramesMulti(
	ctx context.Context,
	tracks [][]PtLL,
	sizePx, total int,
	margin float64,
	bg color.Color,
	trackColors []color.Color,
	base image.Image,
) ([]*PalFrame, []int, error) {

	// общий bbox
	bb := boundsLL{minLat: math.MaxFloat64, minLon: math.MaxFloat64, maxLat: -math.MaxFloat64, maxLon: -math.MaxFloat64}
	for _, pts := range tracks {
		b := bboxLL(pts)
		if b.minLat < bb.minLat { bb.minLat = b.minLat }
		if b.minLon < bb.minLon { bb.minLon = b.minLon }
		if b.maxLat > bb.maxLat { bb.maxLat = b.maxLat }
		if b.maxLon > bb.maxLon { bb.maxLon = b.maxLon }
	}
	padLat := (bb.maxLat - bb.minLat) * margin
	padLon := (bb.maxLon - bb.minLon) * margin
	bb.minLat -= padLat; bb.maxLat += padLat
	bb.minLon -= padLon; bb.maxLon += padLon

	// найдём глобальный диапазон времени
	hasTime := false
	var minT, maxT time.Time
	for _, pts := range tracks {
		for _, p := range pts {
			if p.T == nil { continue }
			if !hasTime {
				minT, maxT = *p.T, *p.T
				hasTime = true
			} else {
				if p.T.Before(minT) { minT = *p.T }
				if p.T.After(maxT)  { maxT = *p.T }
			}
		}
	}

	frames := make([]*PalFrame, 0, total)
	delays := make([]int, 0, total)

	// Если ни у одного трека нет времени — фоллбэк к синхронизации по индексу (как раньше).
	if !hasTime {
		// самый длинный трек задаёт темп
		maxPts := 0
		for _, pts := range tracks { if len(pts) > maxPts { maxPts = len(pts) } }
		if maxPts < 2 { maxPts = 2 }
		step := math.Max(1, float64(maxPts-1)/float64(total))

		for fi := 0; fi < total; fi++ {
			select { case <-ctx.Done(): return nil, nil, ctx.Err(); default: }

			rgba := image.NewRGBA(image.Rect(0, 0, sizePx, sizePx))
			if base != nil {
				draw.Draw(rgba, rgba.Bounds(), base, image.Point{}, draw.Src)
			} else {
				draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
			}

			upto := int(math.Round(step*float64(fi+1)))

			for tIdx, pts := range tracks {
				if len(pts) < 2 { continue }
				endIdx := min(len(pts)-1, upto)
				col := trackColors[tIdx%len(trackColors)]
				for i := 0; i < endIdx; i++ {
					x1, y1 := project(pts[i], bb, sizePx)
					x2, y2 := project(pts[i+1], bb, sizePx)
					drawLineRGBA(rgba, x1, y1, x2, y2, 2, col)
				}
			}

			pimg := image.NewPaletted(rgba.Bounds(), palette.Plan9)
			draw.FloydSteinberg.Draw(pimg, pimg.Bounds(), rgba, image.Point{})
			frames = append(frames, &PalFrame{Img: pimg, Delay: 5}) // 5 → ~20fps
			delays = append(delays, 5)
		}
		return frames, delays, nil
	}

	// Временной режим: кадры равномерно от minT до maxT
	if total < 2 {
		total = 2
	}
	totalDur := maxT.Sub(minT)
	// курсоры по трекам (ускоряет поиск "до frameT")
	cursor := make([]int, len(tracks)) // индекс последней точки <= frameT для каждого трека

	for fi := 0; fi < total; fi++ {
		select { case <-ctx.Done(): return nil, nil, ctx.Err(); default: }

		// момент времени кадра
		var frameT time.Time
		if fi == total-1 {
			frameT = maxT
		} else {
			frameT = minT.Add(time.Duration(float64(totalDur) * float64(fi) / float64(total-1)))
		}

		rgba := image.NewRGBA(image.Rect(0, 0, sizePx, sizePx))
		if base != nil {
			draw.Draw(rgba, rgba.Bounds(), base, image.Point{}, draw.Src)
		} else {
			draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
		}

		for tIdx, pts := range tracks {
			if len(pts) < 2 { continue }
			// продвигаем курсор, пока следующая точка не позже frameT
			i := cursor[tIdx]
			for i+1 < len(pts) {
				tNext := pts[i+1].T
				if tNext == nil || tNext.After(frameT) { break }
				i++
			}
			cursor[tIdx] = i

			endIdx := i
			// если все точки до frameT — рисуем весь трек
			if endIdx >= len(pts)-1 {
				endIdx = len(pts)-1
			}
			if endIdx < 1 { // ещё не стартовал
				continue
			}

			col := trackColors[tIdx%len(trackColors)]
			for k := 0; k < endIdx; k++ {
				x1, y1 := project(pts[k], bb, sizePx)
				x2, y2 := project(pts[k+1], bb, sizePx)
				drawLineRGBA(rgba, x1, y1, x2, y2, 2, col)
			}
		}

		pimg := image.NewPaletted(rgba.Bounds(), palette.Plan9)
		draw.FloydSteinberg.Draw(pimg, pimg.Bounds(), rgba, image.Point{})
		frames = append(frames, &PalFrame{Img: pimg, Delay: 5})
		delays = append(delays, 5)
	}
	return frames, delays, nil
}

type boundsLL struct {
	minLat, maxLat float64
	minLon, maxLon float64
}

func bboxLL(pts []PtLL) boundsLL {
	minLat, maxLat := math.MaxFloat64, -math.MaxFloat64
	minLon, maxLon := math.MaxFloat64, -math.MaxFloat64
	for _, p := range pts {
		if p.Lat < minLat { minLat = p.Lat }
		if p.Lat > maxLat { maxLat = p.Lat }
		if p.Lon < minLon { minLon = p.Lon }
		if p.Lon > maxLon { maxLon = p.Lon }
	}
	return boundsLL{minLat, maxLat, minLon, maxLon}
}

func project(p PtLL, bb boundsLL, size int) (x, y int) {
	spanLat := bb.maxLat - bb.minLat
	spanLon := bb.maxLon - bb.minLon
	xf := 0.0
	if spanLon > 0 { xf = (p.Lon - bb.minLon) / spanLon }
	yf := 0.0
	if spanLat > 0 { yf = 1.0 - (p.Lat-bb.minLat)/spanLat }
	xx := int(math.Round(xf * float64(size-1)))
	yy := int(math.Round(yf * float64(size-1)))
	if xx < 0 { xx = 0 }
	if xx >= size { xx = size - 1 }
	if yy < 0 { yy = 0 }
	if yy >= size { yy = size - 1 }
	return xx, yy
}

func drawLineRGBA(img *image.RGBA, x0, y0, x1, y1, width int, c color.Color) {
	dx := int(math.Abs(float64(x1 - x0)))
	sx := -1; if x0 < x1 { sx = 1 }
	dy := -int(math.Abs(float64(y1 - y0)))
	sy := -1; if y0 < y1 { sy = 1 }
	err := dx + dy
	for {
		plotSquareRGBA(img, x0, y0, width, c)
		if x0 == x1 && y0 == y1 { break }
		e2 := 2 * err
		if e2 >= dy { err += dy; x0 += sx }
		if e2 <= dx { err += dx; y0 += sy }
	}
}

func plotSquareRGBA(img *image.RGBA, cx, cy, w int, c color.Color) {
	if w <= 1 {
		if image.Pt(cx, cy).In(img.Rect) { img.Set(cx, cy, c) }
		return
	}
	r := (w - 1) / 2
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if image.Pt(x, y).In(img.Rect) { img.Set(x, y, c) }
		}
	}
}

func min(a, b int) int { if a < b { return a }; return b }
