package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"math"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// один палетизированный кадр
type PalFrame struct {
	Img   *image.Paletted
	Delay int // hundredths of a second
}

type boundsLL struct {
	minLat, maxLat float64
	minLon, maxLon float64
}

func bboxLL(pts []PtLL) boundsLL {
	minLat, maxLat := math.MaxFloat64, -math.MaxFloat64
	minLon, maxLon := math.MaxFloat64, -math.MaxFloat64
	for _, p := range pts {
		if p.Lat < minLat {
			minLat = p.Lat
		}
		if p.Lat > maxLat {
			maxLat = p.Lat
		}
		if p.Lon < minLon {
			minLon = p.Lon
		}
		if p.Lon > maxLon {
			maxLon = p.Lon
		}
	}
	return boundsLL{minLat, maxLat, minLon, maxLon}
}

// ==== Web Mercator helpers (нормализованные координаты [0..1]) ====

func mercX(lon float64) float64 { // lon (deg) -> [0..1]
	return (lon + 180.0) / 360.0
}

func mercY(lat float64) float64 { // lat (deg) -> [0..1]
	if lat > 85.05112878 {
		lat = 85.05112878
	}
	if lat < -85.05112878 {
		lat = -85.05112878
	}
	r := lat * math.Pi / 180.0
	return 0.5 - math.Log((1+math.Sin(r))/(1-math.Sin(r)))/(4*math.Pi)
}

func invMercY(y float64) float64 { // [0..1] -> lat (deg)
	t := math.Pi * (1 - 2*y)
	return (180 / math.Pi) * math.Atan(math.Sinh(t))
}

// ==== Рендер анимации (используем единый viewport из main.go) ====

func BuildFramesMulti(
	ctx context.Context,
	tracks [][]PtLL,
	sizePx, total int,
	margin float64, // оставлен для совместимости, внутри не используем
	bg color.Color,
	trackColors []color.Color,
	trackWidth int,
	base image.Image,
	bbLL boundsLL, // единый viewport из main.go (после padding/cover/inset)
) ([]*PalFrame, []int, error) {

	// подготовим меркаторный bbox (нормализованные координаты)
	type boundsMerc struct{ minX, minY, maxX, maxY float64 }

	xMin := mercX(bbLL.minLon)
	xMax := mercX(bbLL.maxLon)

	// ВАЖНО: в меркаторе "верх" (maxLat) имеет меньшее значение y, чем "низ" (minLat)
	yTop := mercY(bbLL.maxLat)    // верх кадра
	yBottom := mercY(bbLL.minLat) // низ кадра

	bm := boundsMerc{
		minX: math.Min(xMin, xMax),
		maxX: math.Max(xMin, xMax),
		minY: math.Min(yTop, yBottom), // численно меньшее — это верх
		maxY: math.Max(yTop, yBottom), // численно большее — это низ
	}

	project := func(p PtLL) (int, int) {
		x := mercX(p.Lon)
		y := mercY(p.Lat)
		dx := bm.maxX - bm.minX
		dy := bm.maxY - bm.minY
		if dx == 0 {
			dx = 1e-12
		}
		if dy == 0 {
			dy = 1e-12
		}
		fx := (x - bm.minX) / dx
		fy := (y - bm.minY) / dy
		if fx < 0 {
			fx = 0
		} else if fx > 1 {
			fx = 1
		}
		if fy < 0 {
			fy = 0
		} else if fy > 1 {
			fy = 1
		}
		px := int(math.Round(fx * float64(sizePx-1)))
		py := int(math.Round(fy * float64(sizePx-1)))
		return px, py
	}

	// найдём глобальный диапазон времени
	hasTime := false
	var minT, maxT time.Time
	for _, pts := range tracks {
		for _, p := range pts {
			if p.T == nil {
				continue
			}
			if !hasTime {
				minT, maxT = *p.T, *p.T
				hasTime = true
			} else {
				if p.T.Before(minT) {
					minT = *p.T
				}
				if p.T.After(maxT) {
					maxT = *p.T
				}
			}
		}
	}

	frames := make([]*PalFrame, 0, total)
	delays := make([]int, 0, total)

	// Без времени: синхронизация по индексу
	if !hasTime {
		maxPts := 0
		for _, pts := range tracks {
			if len(pts) > maxPts {
				maxPts = len(pts)
			}
		}
		if maxPts < 2 {
			maxPts = 2
		}
		step := math.Max(1, float64(maxPts-1)/float64(total))

		for fi := 0; fi < total; fi++ {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			default:
			}

			rgba := image.NewRGBA(image.Rect(0, 0, sizePx, sizePx))
			if base != nil {
				draw.Draw(rgba, rgba.Bounds(), base, image.Point{}, draw.Src)
			} else {
				draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
			}

			upto := int(math.Round(step * float64(fi+1)))

			for tIdx, pts := range tracks {
				if len(pts) < 2 {
					continue
				}
				endIdx := min(len(pts)-1, upto)
				col := trackColors[tIdx%len(trackColors)]
				for i := 0; i < endIdx; i++ {
					x1, y1 := project(pts[i])
					x2, y2 := project(pts[i+1])
					drawLineRGBA(rgba, x1, y1, x2, y2, trackWidth, col)
				}
			}

			pimg := image.NewPaletted(rgba.Bounds(), palette.Plan9)
			draw.FloydSteinberg.Draw(pimg, pimg.Bounds(), rgba, image.Point{})
			frames = append(frames, &PalFrame{Img: pimg, Delay: 5}) // 5 → ~20fps
			delays = append(delays, 5)
		}
		return frames, delays, nil
	}

	// С временем: равномерно от minT до maxT
	if total < 2 {
		total = 2
	}
	totalDur := maxT.Sub(minT)
	cursor := make([]int, len(tracks)) // индекс последней точки <= frameT

	for fi := 0; fi < total; fi++ {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

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
			if len(pts) < 2 {
				continue
			}

			i := cursor[tIdx]
			for i+1 < len(pts) {
				tNext := pts[i+1].T
				if tNext == nil || tNext.After(frameT) {
					break
				}
				i++
			}
			cursor[tIdx] = i

			endIdx := i
			if endIdx >= len(pts)-1 {
				endIdx = len(pts) - 1
			}
			if endIdx < 1 {
				continue
			}

			col := trackColors[tIdx%len(trackColors)]
			for k := 0; k < endIdx; k++ {
				x1, y1 := project(pts[k])
				x2, y2 := project(pts[k+1])
				drawLineRGBA(rgba, x1, y1, x2, y2, trackWidth, col)
			}
		}

		pimg := image.NewPaletted(rgba.Bounds(), palette.Plan9)
		draw.FloydSteinberg.Draw(pimg, pimg.Bounds(), rgba, image.Point{})
		frames = append(frames, &PalFrame{Img: pimg, Delay: 5})
		delays = append(delays, 5)
	}
	return frames, delays, nil
}

// ===== низкоуровневый рисовальщик толстой линии (квадратная «кисть») =====

func drawLineRGBA(img *image.RGBA, x0, y0, x1, y1, width int, c color.Color) {
	dx := int(math.Abs(float64(x1 - x0)))
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := -int(math.Abs(float64(y1 - y0)))
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		plotSquareRGBA(img, x0, y0, width, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func plotSquareRGBA(img *image.RGBA, cx, cy, w int, c color.Color) {
	if w <= 1 {
		if image.Pt(cx, cy).In(img.Rect) {
			img.Set(cx, cy, c)
		}
		return
	}
	r := (w - 1) / 2
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if image.Pt(x, y).In(img.Rect) {
				img.Set(x, y, c)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

/*** ===================== HUD (легенда + скорости) ===================== ***/

type hudTrack struct {
	Name   string
	Color  color.Color
	Speeds []float64 // м/с
	Pts    []PtLL    // для доступа к T и длине
}

func hudDrawLegend(dst draw.Image, tracks []hudTrack) {
	if len(tracks) == 0 {
		return
	}
	padding := 10
	lineH := 16
	swatch := 12

	face := basicfont.Face7x13
	maxW := 0
	for _, tr := range tracks {
		w := font.MeasureString(face, tr.Name).Ceil()
		if w > maxW {
			maxW = w
		}
	}
	boxW := padding*3 + swatch + maxW
	boxH := padding*2 + lineH*len(tracks)

	fillRect(dst, image.Rect(padding, padding, padding+boxW, padding+boxH), color.RGBA{0, 0, 0, 255})

	x := padding * 2
	y := padding + lineH
	for _, tr := range tracks {
		fillRect(dst, image.Rect(x, y-swatch+3, x+swatch, y+3), tr.Color)
		drawString(dst, x+swatch+6, y, tr.Name, color.RGBA{255, 255, 255, 255})
		y += lineH
	}
}

func hudDrawSpeeds(dst draw.Image, tracks []hudTrack, tNow time.Time, units string) {
	if len(tracks) == 0 {
		return
	}
	padding := 10
	lineH := 16
	face := basicfont.Face7x13
	unitLabel := map[string]string{"kmh": "км/ч", "ms": "м/с", "mph": "mph"}[units]
	if unitLabel == "" {
		unitLabel = "м/с"
	}

	lines := make([]string, 0, len(tracks))
	maxW := 0
	for _, tr := range tracks {
		idx := lastIndexBeforeOrAtPt(tr.Pts, tNow)
		speed := 0.0
		if idx >= 0 && idx < len(tr.Speeds) {
			speed = convertSpeed(tr.Speeds[idx], units)
		}
		line := fmt.Sprintf("%s — %.1f %s", tr.Name, speed, unitLabel)
		lines = append(lines, line)
		w := font.MeasureString(face, line).Ceil()
		if w > maxW {
			maxW = w
		}
	}

	boxW := padding*2 + maxW
	boxH := padding*2 + lineH*len(lines)

	b := dst.Bounds()
	x0 := b.Max.X - boxW - padding
	y0 := padding
	fillRect(dst, image.Rect(x0, y0, x0+boxW, y0+boxH), color.RGBA{0, 0, 0, 255})

	x := x0 + padding
	y := y0 + lineH
	for i, tr := range tracks {
		fillRect(dst, image.Rect(x-10, y-8, x-4, y-2), tr.Color)
		drawString(dst, x, y, lines[i], color.RGBA{255, 255, 255, 255})
		y += lineH
	}
}

// ===== мелкие хелперы для HUD =====

func fillRect(dst draw.Image, r image.Rectangle, c color.Color) {
	draw.Draw(dst, r, &image.Uniform{C: c}, image.Point{}, draw.Over)
}

func drawString(dst draw.Image, x, y int, s string, col color.Color) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{C: col},
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

func lastIndexBeforeOrAtPt(pts []PtLL, t time.Time) int {
	lo, hi := 0, len(pts)-1
	ans := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if pts[mid].T == nil || pts[mid].T.After(t) {
			hi = mid - 1
		} else {
			ans = mid
			lo = mid + 1
		}
	}
	return ans
}
