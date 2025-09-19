package main

import (
	"errors"
	"image/color"
	"strconv"
	"strings"
)

// один цвет
func ParseHexColor(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return nil, errors.New("hex color должен начинаться с #")
	}
	h := strings.TrimPrefix(s, "#")
	if len(h) == 6 {
		r, _ := strconv.ParseUint(h[0:2], 16, 8)
		g, _ := strconv.ParseUint(h[2:4], 16, 8)
		b, _ := strconv.ParseUint(h[4:6], 16, 8)
		return color.RGBA{uint8(r), uint8(g), uint8(b), 0xFF}, nil
	}
	if len(h) == 8 {
		a, _ := strconv.ParseUint(h[0:2], 16, 8)
		r, _ := strconv.ParseUint(h[2:4], 16, 8)
		g, _ := strconv.ParseUint(h[4:6], 16, 8)
		b, _ := strconv.ParseUint(h[6:8], 16, 8)
		return color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)}, nil
	}
	return nil, errors.New("hex color формат: #RRGGBB или #AARRGGBB")
}

// список цветов через запятую
func ParseHexColors(csv string) ([]color.Color, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" { return nil, nil }
	parts := strings.Split(csv, ",")
	out := make([]color.Color, 0, len(parts))
	for _, p := range parts {
		c, err := ParseHexColor(strings.TrimSpace(p))
		if err != nil { return nil, err }
		out = append(out, c)
	}
	return out, nil
}
