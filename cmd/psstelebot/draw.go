package main

import (
	"image"
	"image/color"
	"math"
)

type Pt struct{ X, Y int }
type PtF struct{ X, Y float64 }

// --- Блендинг прямого альфа-канала поверх RGBA ---
func blendOver(dst *image.RGBA, x, y int, c color.NRGBA, cov float64) {
	if x < dst.Rect.Min.X || x >= dst.Rect.Max.X || y < dst.Rect.Min.Y || y >= dst.Rect.Max.Y {
		return
	}
	if cov <= 0 {
		return
	}
	i := dst.PixOffset(x, y)

	da := float64(dst.Pix[i+3]) / 255.0
	a := float64(c.A)/255.0 * cov
	outA := a + da*(1-a)
	if outA <= 1e-6 {
		dst.Pix[i+0], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = 0, 0, 0, 0
		return
	}

	dr := float64(dst.Pix[i+0])
	dg := float64(dst.Pix[i+1])
	db := float64(dst.Pix[i+2])
	sr := float64(c.R)
	sg := float64(c.G)
	sb := float64(c.B)

	dst.Pix[i+0] = byte(clamp255((sr*a + dr*da*(1-a)) / outA))
	dst.Pix[i+1] = byte(clamp255((sg*a + dg*da*(1-a)) / outA))
	dst.Pix[i+2] = byte(clamp255((sb*a + db*da*(1-a)) / outA))
	dst.Pix[i+3] = byte(clamp255(outA * 255.0))
}

func clamp255(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return x
}

func fpart(x float64) float64  { return x - math.Floor(x) }
func rfpart(x float64) float64 { return 1 - fpart(x) }

// Xiaolin Wu — антиалиасная тонкая линия
func lineAA(dst *image.RGBA, x0, y0, x1, y1 float64, col color.NRGBA) {
	steep := math.Abs(y1-y0) > math.Abs(x1-x0)
	if steep {
		x0, y0 = y0, x0
		x1, y1 = y1, x1
	}
	if x0 > x1 {
		x0, x1 = x1, x0
		y0, y1 = y1, y0
	}
	dx := x1 - x0
	grad := 0.0
	if dx != 0 {
		grad = (y1 - y0) / dx
	}

	xend := math.Round(x0)
	yend := y0 + grad*(xend-x0)
	xgap := rfpart(x0 + 0.5)
	xpxl1 := int(xend)
	ypxl1 := int(math.Floor(yend))
	if steep {
		blendOver(dst, ypxl1, xpxl1, col, rfpart(yend)*xgap)
		blendOver(dst, ypxl1+1, xpxl1, col, fpart(yend)*xgap)
	} else {
		blendOver(dst, xpxl1, ypxl1, col, rfpart(yend)*xgap)
		blendOver(dst, xpxl1, ypxl1+1, col, fpart(yend)*xgap)
	}
	intery := yend + grad

	xend2 := math.Round(x1)
	yend2 := y1 + grad*(xend2-x1)
	xgap2 := fpart(x1 + 0.5)
	xpxl2 := int(xend2)
	ypxl2 := int(math.Floor(yend2))

	if steep {
		for x := xpxl1 + 1; x <= xpxl2-1; x++ {
			yy := int(math.Floor(intery))
			blendOver(dst, yy, x, col, rfpart(intery))
			blendOver(dst, yy+1, x, col, fpart(intery))
			intery += grad
		}
		blendOver(dst, ypxl2, xpxl2, col, rfpart(yend2)*xgap2)
		blendOver(dst, ypxl2+1, xpxl2, col, fpart(yend2)*xgap2)
	} else {
		for x := xpxl1 + 1; x <= xpxl2-1; x++ {
			yy := int(math.Floor(intery))
			blendOver(dst, x, yy, col, rfpart(intery))
			blendOver(dst, x, yy+1, col, fpart(intery))
			intery += grad
		}
		blendOver(dst, xpxl2, ypxl2, col, rfpart(yend2)*xgap2)
		blendOver(dst, xpxl2, ypxl2+1, col, fpart(yend2)*xgap2)
	}
}

func fillDiscAA(dst *image.RGBA, cx, cy int, r float64, col color.NRGBA) {
	if r <= 0.5 {
		blendOver(dst, cx, cy, col, 1)
		return
	}
	minY := int(math.Floor(float64(cy) - r))
	maxY := int(math.Ceil(float64(cy) + r))
	for y := minY; y <= maxY; y++ {
		dy := math.Abs(float64(y) - float64(cy))
		if dy > r {
			continue
		}
		wx := math.Sqrt(r*r - dy*dy)
		x0 := float64(cx) - wx
		x1 := float64(cx) + wx
		for x := int(math.Ceil(x0)); x <= int(math.Floor(x1)); x++ {
			blendOver(dst, x, y, col, 1.0)
		}
		blendOver(dst, int(math.Floor(x0)), y, col, rfpart(x0))
		blendOver(dst, int(math.Ceil(x1)), y, col, fpart(x1))
	}
}

// Публичная: рисуем сегмент по **float** координатам с толщиной и круглыми торцами.
func drawSegmentRGBA(dst *image.RGBA, a, b PtF, col color.NRGBA, width float32) {
	w := float64(width)
	if w < 1 {
		w = 1
	}
	ax, ay := a.X, a.Y
	bx, by := b.X, b.Y

	dx, dy := bx-ax, by-ay
	L := math.Hypot(dx, dy)
	if L < 1e-6 {
		fillDiscAA(dst, int(math.Round(ax)), int(math.Round(ay)), w/2, col)
		return
	}
	nx, ny := -dy/L, dx/L

	if w <= 1.01 {
		lineAA(dst, ax, ay, bx, by, col)
		fillDiscAA(dst, int(math.Round(ax)), int(math.Round(ay)), 0.5, col)
		fillDiscAA(dst, int(math.Round(bx)), int(math.Round(by)), 0.5, col)
		return
	}

	half := (w - 1.0) / 2.0
	maxOffset := math.Ceil(half)
	for o := -maxOffset; o <= maxOffset; o++ {
		t := float64(o)
		edge := math.Max(0, 1.0-math.Abs(t)/(half+0.001))
		cc := col
		cc.A = uint8(edge * float64(col.A))
		ox := nx * t
		oy := ny * t
		lineAA(dst, ax+ox, ay+oy, bx+ox, by+oy, cc)
	}
	fillDiscAA(dst, int(math.Round(ax)), int(math.Round(ay)), w/2, col)
	fillDiscAA(dst, int(math.Round(bx)), int(math.Round(by)), w/2, col)
}

func dirtyRectF(a, b PtF, pad float64) image.Rectangle {
	minx, maxx := a.X, b.X
	if b.X < a.X {
		minx, maxx = b.X, a.X
	}
	miny, maxy := a.Y, b.Y
	if b.Y < a.Y {
		miny, maxy = b.Y, a.Y
	}
	return image.Rect(
		int(math.Floor(minx-pad)),
		int(math.Floor(miny-pad)),
		int(math.Ceil(maxx+pad))+1,
		int(math.Ceil(maxy+pad))+1,
	)
}
