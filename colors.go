package main

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

func ParseHexColor(hex string) (color.Color, error) {
	hex = strings.TrimSpace(hex)
	if strings.HasPrefix(hex, "#") {
		hex = hex[1:]
	}
	switch len(hex) {
	case 3: // rgb
		r, err := strconv.ParseUint(strings.Repeat(string(hex[0]), 2), 16, 8)
		if err != nil {
			return nil, err
		}
		g, err := strconv.ParseUint(strings.Repeat(string(hex[1]), 2), 16, 8)
		if err != nil {
			return nil, err
		}
		b, err := strconv.ParseUint(strings.Repeat(string(hex[2]), 2), 16, 8)
		if err != nil {
			return nil, err
		}
		return color.RGBA{uint8(r), uint8(g), uint8(b), 255}, nil
	case 6: // rrggbb
		r, err := strconv.ParseUint(hex[0:2], 16, 8)
		if err != nil {
			return nil, err
		}
		g, err := strconv.ParseUint(hex[2:4], 16, 8)
		if err != nil {
			return nil, err
		}
		b, err := strconv.ParseUint(hex[4:6], 16, 8)
		if err != nil {
			return nil, err
		}
		return color.RGBA{uint8(r), uint8(g), uint8(b), 255}, nil
	case 8: // rrggbbaa
		r, err := strconv.ParseUint(hex[0:2], 16, 8)
		if err != nil {
			return nil, err
		}
		g, err := strconv.ParseUint(hex[2:4], 16, 8)
		if err != nil {
			return nil, err
		}
		b, err := strconv.ParseUint(hex[4:6], 16, 8)
		if err != nil {
			return nil, err
		}
		a, err := strconv.ParseUint(hex[6:8], 16, 8)
		if err != nil {
			return nil, err
		}
		return color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)}, nil
	default:
		return nil, fmt.Errorf("invalid hex color: %s", hex)
	}
}

func ParseHexColors(csv string) ([]color.Color, error) {
	parts := strings.Split(csv, ",")
	out := make([]color.Color, 0, len(parts))
	for _, s := range parts {
		if strings.TrimSpace(s) == "" {
			continue
		}
		c, err := ParseHexColor(s)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
