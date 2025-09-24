package main

import (
	"image"
	"image/color"
)

// buildQuickPaletteWithColors — простая палитра: базовые тона + выборка с фона + принудительные цвета линий.
func buildQuickPaletteWithColors(samples []image.Image, mustHave []color.Color, n int) color.Palette {
	p := color.Palette{
		color.Black, color.White,
		color.NRGBA{0x20, 0x20, 0x20, 0xff},
		color.NRGBA{0xc0, 0xc0, 0xc0, 0xff},
	}
	// добавим обязательные цвета (линии треков)
	for _, c := range mustHave {
		if len(p) >= n {
			break
		}
		p = append(p, color.NRGBAModel.Convert(c).(color.NRGBA))
	}
	// немного образцов фона — для мягкого dithering
	if len(samples) > 0 && samples[0] != nil {
		b := samples[0].Bounds()
		stepX := max(1, b.Dx()/8)
		stepY := max(1, b.Dy()/8)
		for y := b.Min.Y; y < b.Max.Y && len(p) < n; y += stepY {
			for x := b.Min.X; x < b.Max.X && len(p) < n; x += stepX {
				p = append(p, color.NRGBAModel.Convert(samples[0].At(x, y)).(color.NRGBA))
			}
		}
	}
	if len(p) > n {
		return p[:n]
	}
	return p
}
