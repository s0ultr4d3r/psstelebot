package main

import (
	"image/gif"
	"io"
)

func writeGIFAll(w io.Writer, frames []*PalFrame, delays []int) error {
	g := &gif.GIF{}
	for i := range frames {
		g.Image = append(g.Image, frames[i].Img)
		g.Delay = append(g.Delay, delays[i])
	}
	g.Disposal = make([]byte, len(g.Image))
	for i := range g.Disposal {
		g.Disposal[i] = gif.DisposalNone
	}
	return gif.EncodeAll(w, g)
}
