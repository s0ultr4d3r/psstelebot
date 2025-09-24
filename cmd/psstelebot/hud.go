package main

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type hudTrack struct {
	Name   string
	Color  color.Color
	Speeds []float64
	Pts    []PtLL
}

/************ Масштаб HUD ************/

// выбираем масштаб HUD по размеру конечного кадра
func hudScaleFor(px int) int {
	switch {
	case px >= 2200:
		return 3
	case px >= 1200:
		return 2
	default:
		return 1
	}
}

/************ Рисование HUD (возвращают dirty-rect) ************/

// ЛЕГЕНДА (нижний-левый угол)
func hudPaintLegend(dst draw.Image, tracks []hudTrack, scale int) image.Rectangle {
	if len(tracks) == 0 || dst == nil {
		return image.Rectangle{}
	}
	b := dst.Bounds()
	padding := 8 * scale
	rowH := 14 * scale
	swatch := 10 * scale
	innerPad := 6 * scale

	names := make([]string, len(tracks))
	for i, t := range tracks {
		n := filepath.Base(t.Name)
		if n == "" {
			n = "track"
		}
		if len(n) > 38 {
			n = n[:35] + "..."
		}
		names[i] = n
	}
	maxw := 0
	for _, n := range names {
		if w := textWidthScaled(n, scale); w > maxw {
			maxw = w
		}
	}
	boxW := padding*2 + innerPad + swatch + 6*scale + maxw
	boxH := padding*2 + rowH*len(tracks)

	x0 := b.Min.X + padding
	y0 := b.Max.Y - boxH - padding
	r := image.Rect(x0, y0, x0+boxW, y0+boxH)

	fillRect(dst, r, color.NRGBA{0, 0, 0, 0xff})
	strokeRect(dst, r, color.NRGBA{200, 200, 200, 0xff})

	x := x0 + padding + innerPad
	y := y0 + padding
	for i, t := range tracks {
		cc := toNRGBA(t.Color)
		fillRect(dst, image.Rect(x, y+2*scale, x+swatch, y+2*scale+swatch), cc)
		drawStringScaled(dst, x+swatch+6*scale, y+2*scale, names[i], color.NRGBA{230, 230, 230, 0xff}, scale)
		y += rowH
	}
	return r
}

// СКОРОСТИ (верхний-левый угол)
func hudPaintSpeeds(dst draw.Image, tracks []hudTrack, tNow time.Time, units string, scale int) image.Rectangle {
	if len(tracks) == 0 || dst == nil || tNow.IsZero() {
		return image.Rectangle{}
	}
	b := dst.Bounds()
	padding := 8 * scale
	rowH := 14 * scale
	innerPad := 6 * scale
	swatch := 6 * scale

	lines := make([]string, 0, len(tracks))
	cols := make([]color.NRGBA, 0, len(tracks))
	for _, tr := range tracks {
		v := currentSpeedAt(tr, tNow)
		val := convertSpeed(v, units)
		unitLabel := units
		if unitLabel == "ms" {
			unitLabel = "m/s"
		}
		name := filepath.Base(tr.Name)
		if name == "" {
			name = "track"
		}
		name = strings.TrimSuffix(name, filepath.Ext(name))
		if len(name) > 16 {
			name = name[:13] + "..."
		}
		lines = append(lines, name+"  "+formatFloat1(val)+" "+unitLabel)
		cols = append(cols, toNRGBA(tr.Color))
	}

	maxw := 0
	for _, s := range lines {
		if w := textWidthScaled(s, scale); w > maxw {
			maxw = w
		}
	}
	boxW := padding*2 + innerPad*2 + swatch + 6*scale + maxw
	boxH := padding*2 + rowH*len(lines)

	x0 := b.Min.X + padding
	y0 := b.Min.Y + padding
	r := image.Rect(x0, y0, x0+boxW, y0+boxH)

	fillRect(dst, r, color.NRGBA{0, 0, 0, 0xff})
	strokeRect(dst, r, color.NRGBA{200, 200, 200, 0xff})

	x := x0 + padding + innerPad
	y := y0 + padding
	for i, s := range lines {
		fillRect(dst, image.Rect(x, y+3*scale, x+swatch, y+3*scale+swatch), cols[i])
		drawStringScaled(dst, x+swatch+6*scale, y+2*scale, s, color.NRGBA{235, 235, 235, 0xff}, scale)
		y += rowH
	}
	return r
}

/************ Скорость в момент времени ************/

func currentSpeedAt(tr hudTrack, t time.Time) float64 {
	n := len(tr.Pts)
	if n == 0 || len(tr.Speeds) != n {
		return 0
	}
	// бинарный поиск последней точки с T<=t
	lo, hi := 0, n-1
	pos := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		pt := tr.Pts[mid]
		if pt.T == nil || !pt.T.After(t) {
			pos = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	// интерполяция, если возможна
	if pos+1 < n && tr.Pts[pos].T != nil && tr.Pts[pos+1].T != nil {
		t0 := *tr.Pts[pos].T
		t1 := *tr.Pts[pos+1].T
		if t1.After(t0) {
			f := float64(t.Sub(t0)) / float64(t1.Sub(t0))
			if f < 0 {
				f = 0
			}
			if f > 1 {
				f = 1
			}
			return tr.Speeds[pos]*(1-f) + tr.Speeds[pos+1]*f
		}
	}
	if tr.Speeds[pos] > 0 {
		return tr.Speeds[pos]
	}
	// fallback на ближайшее ненулевое
	for r := 1; r <= 5; r++ {
		if i := pos + r; i < n && tr.Speeds[i] > 0 {
			return tr.Speeds[i]
		}
		if i := pos - r; i >= 0 && tr.Speeds[i] > 0 {
			return tr.Speeds[i]
		}
	}
	return 0
}

/************ Вспомогательные ************/

func formatFloat1(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if strings.HasSuffix(s, ".0") {
		return s[:len(s)-2]
	}
	return s
}

func fillRect(dst draw.Image, r image.Rectangle, c color.NRGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			dst.Set(x, y, c)
		}
	}
}

func strokeRect(dst draw.Image, r image.Rectangle, c color.NRGBA) {
	for x := r.Min.X; x < r.Max.X; x++ {
		dst.Set(x, r.Min.Y, c)
		dst.Set(x, r.Max.Y-1, c)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dst.Set(r.Min.X, y, c)
		dst.Set(r.Max.X-1, y, c)
	}
}

func toNRGBA(c color.Color) color.NRGBA { return color.NRGBAModel.Convert(c).(color.NRGBA) }

/************ Мини-шрифт 5×7 c масштабом ************/

var font5x7 = map[rune][7]byte{
	' ': {0, 0, 0, 0, 0, 0, 0},
	'.': {0, 0, 0, 0, 0, 0, 0x10},
	',': {0, 0, 0, 0, 0, 0x10, 0x20},
	'-': {0, 0, 0, 0x1C, 0, 0, 0},
	':': {0, 0, 0x10, 0, 0x10, 0, 0},
	'?': {0x0E, 0x11, 0x01, 0x06, 0x04, 0, 0x04},
	'/': {0x02, 0x04, 0x08, 0x10, 0x20, 0, 0},
	'0': {0x1E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x1E},
	'1': {0x04, 0x0C, 0x14, 0x04, 0x04, 0x04, 0x1F},
	'2': {0x1E, 0x01, 0x01, 0x0E, 0x10, 0x10, 0x1F},
	'3': {0x1E, 0x01, 0x01, 0x0E, 0x01, 0x01, 0x1E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x01, 0x1E},
	'6': {0x0E, 0x10, 0x1E, 0x11, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x01, 0x0E},
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	'D': {0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0E},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'I': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x1F},
	'J': {0x1F, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0E},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x0A, 0x0A, 0x04, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
	'_': {0, 0, 0, 0, 0, 0, 0x1F},
	'k': {0x10, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'h': {0x10, 0x10, 0x16, 0x19, 0x11, 0x11, 0x11},
	'm': {0, 0, 0x1A, 0x15, 0x15, 0x11, 0x11},
}

func drawCharScaled(dst draw.Image, x, y int, r rune, col color.NRGBA, scale int) int {
	mask, ok := font5x7[r]
	if !ok {
		mask = font5x7['?']
	}
	for row := 0; row < 7; row++ {
		line := mask[row]
		for colbit := 0; colbit < 5; colbit++ {
			if (line>>(4-colbit))&1 == 1 {
				// «пиксель» шрифта — это квадрат scale×scale
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						dst.Set(x+colbit*scale+dx, y+row*scale+dy, col)
					}
				}
			}
		}
	}
	return 6 * scale
}

func drawStringScaled(dst draw.Image, x, y int, s string, col color.NRGBA, scale int) {
	xx := x
	for _, r := range s {
		xx += drawCharScaled(dst, xx, y, toUpperSafe(r), col, scale)
	}
}

func textWidthScaled(s string, scale int) int { return 6 * scale * len([]rune(s)) }

func toUpperSafe(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}
