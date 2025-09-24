package main

import (
	"image"
	"image/color"
	"image/gif"
	"io"

	"golang.org/x/image/draw"
)

type PalFrame struct {
	Img  *image.Paletted // палеттированный кусок (SubImage)
	Rect image.Rectangle // его положение на общем холсте
}

// --- Палеттизация с учётом смещения исходника ---

// toPalettedOpaque — для ПЕРВОГО кадра (непрозрачный, без прозрачного индекса)
func toPalettedOpaque(src image.Image, pal color.Palette) *image.Paletted {
	sb := src.Bounds()
	dst := image.NewPaletted(image.Rect(0, 0, sb.Dx(), sb.Dy()), pal)
	draw.FloydSteinberg.Draw(dst, dst.Rect, src, sb.Min)
	return dst
}

// toPalettedTransparent — для dirty-rect кадров:
// палитра начинается с прозрачного цвета (индекс 0),
// «пустые» пиксели становятся прозрачными и не затирают карту.
func toPalettedTransparent(src image.Image, pal color.Palette) *image.Paletted {
	// соберём палитру: [transparent] + первые (<=255) цветов из pal
	transparent := color.NRGBA{0, 0, 0, 0}
	newPal := make(color.Palette, 0, 256)
	newPal = append(newPal, transparent)
	for _, c := range pal {
		if len(newPal) >= 256 {
			break
		}
		// избегаем второго прозрачного
		if nc, ok := c.(color.NRGBA); ok && nc.A == 0 {
			continue
		}
		newPal = append(newPal, c)
	}

	sb := src.Bounds()
	dst := image.NewPaletted(image.Rect(0, 0, sb.Dx(), sb.Dy()), newPal)
	// Важно: рисуем со смещением src (sb.Min), иначе субизображения «съедут»
	draw.FloydSteinberg.Draw(dst, dst.Rect, src, sb.Min)
	return dst
}

// --- Запись GIF с частичными кадрами ---
func writeGIFAll(w io.Writer, frames []*PalFrame, delays []int) error {
	if len(frames) == 0 {
		return gif.EncodeAll(w, &gif.GIF{})
	}

	// Размер общего холста
	W, H := 0, 0
	for _, f := range frames {
		if f.Rect.Max.X > W {
			W = f.Rect.Max.X
		}
		if f.Rect.Max.Y > H {
			H = f.Rect.Max.Y
		}
	}
	if W == 0 || H == 0 {
		b := frames[0].Img.Bounds()
		if b.Max.X > W {
			W = b.Max.X
		}
		if b.Max.Y > H {
			H = b.Max.Y
		}
	}

	g := &gif.GIF{
		Image:    make([]*image.Paletted, 0, len(frames)),
		Delay:    make([]int, 0, len(frames)),
		Disposal: make([]byte, 0, len(frames)),
	}
	g.Config.Width, g.Config.Height = W, H

	canvas := image.Rect(0, 0, W, H)
	for i, f := range frames {
		r := f.Rect

		// подстраховка: не выходить за границы холста
		if !r.In(canvas) {
			r = r.Intersect(canvas)
			if r.Empty() {
				r = image.Rect(0, 0, 1, 1)
				f.Img = image.NewPaletted(r, f.Img.Palette)
			} else {
				sb := f.Img.Bounds()
				sub := image.Rect(0, 0, min(r.Dx(), sb.Dx()), min(r.Dy(), sb.Dy()))
				f.Img = f.Img.SubImage(sub).(*image.Paletted)
			}
		}
		f.Img.Rect = r

		g.Image = append(g.Image, f.Img)
		g.Delay = append(g.Delay, delays[i])

		// Накапливаем поверх предыдущего
		// (фон остаётся, partial-кадры добавляют только линии)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	return gif.EncodeAll(w, g)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
