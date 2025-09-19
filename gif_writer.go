package main

import (
	"image"
	"image/gif"
	"io"
)

func writeGIFAll(w io.Writer, frames []*PalFrame, delays []int) error {
	g := &gif.GIF{
		Image:     make([]*image.Paletted, len(frames)),
		Delay:     make([]int, len(frames)),
		LoopCount: 0,
	}
	for i, pf := range frames {
		g.Image[i] = pf.Img
		g.Delay[i] = delays[i]
	}
	return gif.EncodeAll(w, g)
}
