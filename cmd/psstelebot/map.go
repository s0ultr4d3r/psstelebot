package main

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/s0ultr4d3r/psstelebot/tiles"
)

// BuildFramesMulti:
//   1) первый кадр — полный фон (непрозрачный);
//   2) далее — дельты (прозрачный слой) + HUD, без «плиток»;
//   3) треки синхронизированы по времени t0..t1;
//   4) задержка кадров (delayCS) приходит снаружи (100/fps);
//   5) имена треков используются в HUD.
func BuildFramesMulti(
	_ context.Context,
	tracks [][]PtLL,
	px int,
	totalFrames int,
	_ float64,
	bg color.Color,
	trackColors []color.Color,
	lineWidth int,
	baseImg image.Image,
	bbLLFit boundsLL,
	trackNames []string,
	delayCS int,
) ([]*PalFrame, []int, error) {

	// --- фон ---
	var base *image.RGBA
	if baseImg != nil {
		if baseImg.Bounds().Dx() != px || baseImg.Bounds().Dy() != px {
			dst := image.NewRGBA(image.Rect(0, 0, px, px))
			xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), baseImg, baseImg.Bounds(), xdraw.Over, nil)
			base = dst
		} else if b, ok := baseImg.(*image.RGBA); ok {
			base = b
		} else {
			dst := image.NewRGBA(image.Rect(0, 0, px, px))
			xdraw.Copy(dst, dst.Bounds().Min, baseImg, baseImg.Bounds(), xdraw.Over, nil)
			base = dst
		}
	} else {
		base = image.NewRGBA(image.Rect(0, 0, px, px))
		draw.Draw(base, base.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	}
	// накопительный холст
	work := image.NewRGBA(base.Bounds())
	copy(work.Pix, base.Pix)

	// --- проекция (float) ---
	x0 := tiles.MercX(bbLLFit.minLon)
	x1 := tiles.MercX(bbLLFit.maxLon)
	yTop := tiles.MercY(bbLLFit.maxLat)
	yBot := tiles.MercY(bbLLFit.minLat)
	scaleX := float64(px) / (x1 - x0)
	scaleY := float64(px) / (yBot - yTop)

	project := func(p PtLL) PtF {
		wx := tiles.MercX(p.Lon)
		wy := tiles.MercY(p.Lat)
		return PtF{
			X: (wx - x0) * scaleX,
			Y: (wy - yTop) * scaleY,
		}
	}

	type projTrack struct {
		ptsF []PtF
		col  color.NRGBA
	}
	proj := make([]projTrack, 0, len(tracks))
	for i, tr := range tracks {
		if len(tr) == 0 {
			continue
		}
		ps := make([]PtF, 0, len(tr))
		for _, p := range tr {
			ps = append(ps, project(p))
		}
		c := color.NRGBAModel.Convert(trackColors[i%len(trackColors)]).(color.NRGBA)
		proj = append(proj, projTrack{ptsF: ps, col: c})
	}

	// --- палитра (фон + образцы линий + фикс-цвета линий) ---
	samples := []image.Image{base}
	tmp := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(proj) && i < 4; i++ {
		drawSegmentRGBA(tmp, PtF{4, float64(8 + i*10)}, PtF{60, float64(8 + i*10)}, proj[i].col, float32(lineWidth))
	}
	extra := make([]color.Color, 0, len(proj))
	for i := range proj {
		extra = append(extra, proj[i].col)
	}
	pal := buildQuickPaletteWithColors(samples, extra, 256)

	// --- первый кадр (полный фон) ---
	frames := make([]*PalFrame, 0, totalFrames+1)
	delays := make([]int, 0, totalFrames+1)

	full := toPalettedOpaque(base, pal)
	full.Rect = base.Bounds()
	frames = append(frames, &PalFrame{Img: full, Rect: full.Rect})
	delays = append(delays, maxInt(delayCS, 1))

	// --- t0..t1 по всем трекам ---
	hasTime := false
	var t0, t1 time.Time
	for _, tr := range tracks {
		for _, p := range tr {
			if p.T == nil {
				continue
			}
			if !hasTime {
				t0, t1, hasTime = *p.T, *p.T, true
			} else {
				if p.T.Before(t0) {
					t0 = *p.T
				}
				if p.T.After(t1) {
					t1 = *p.T
				}
			}
		}
	}

	// --- HUD-треки (с именами) ---
	hudTracks := make([]hudTrack, 0, len(tracks))
	for i := range tracks {
		name := ""
		if i < len(trackNames) {
			name = trackNames[i]
		}
		hudTracks = append(hudTracks, hudTrack{
			Name:   name,
			Color:  proj[i].col,
			Speeds: computeSpeedsPtLL(tracks[i], 5),
			Pts:    tracks[i],
		})
	}

	// --- цикл кадров: прозрачный слой дельт + HUD ---
	type cursor struct{ i int }
	cur := make([]cursor, len(proj))
	pad := math.Ceil(float64(lineWidth)*1.2 + 2.0)
	hudScale := hudScaleFor(px)

	for f := 0; f < totalFrames; f++ {
		layer := image.NewRGBA(work.Bounds()) // прозрачный слой кадра
		var dirty image.Rectangle
		drew := false

		// текущий момент времени
		var tNow time.Time
		if hasTime {
			if f == totalFrames-1 {
				tNow = t1
			} else {
				frac := float64(f) / float64(totalFrames-1)
				tNow = t0.Add(time.Duration(frac * float64(t1.Sub(t0))))
			}
		}

		for ti := range proj {
			ptsF := proj[ti].ptsF
			src := tracks[ti]
			if len(ptsF) < 2 {
				continue
			}

			if hasTime {
				for cur[ti].i+1 < len(src) {
					nxt := src[cur[ti].i+1].T
					if nxt == nil || nxt.After(tNow) {
						break
					}
					a := ptsF[cur[ti].i]
					b := ptsF[cur[ti].i+1]

					drawSegmentRGBA(layer, a, b, proj[ti].col, float32(lineWidth))
					draw.Draw(work, work.Bounds(), layer, image.Point{}, draw.Over)

					r := dirtyRectF(a, b, pad)
					if dirty.Empty() {
						dirty = r
					} else {
						dirty = dirty.Union(r)
					}
					cur[ti].i++
					drew = true
				}
			} else {
				target := int(math.Round(float64(f+1) / float64(totalFrames) * float64(len(ptsF)-1)))
				for cur[ti].i < target {
					a := ptsF[cur[ti].i]
					b := ptsF[cur[ti].i+1]

					drawSegmentRGBA(layer, a, b, proj[ti].col, float32(lineWidth))
					draw.Draw(work, work.Bounds(), layer, image.Point{}, draw.Over)

					r := dirtyRectF(a, b, pad)
					if dirty.Empty() {
						dirty = r
					} else {
						dirty = dirty.Union(r)
					}
					cur[ti].i++
					drew = true
				}
			}
		}

		// HUD на том же прозрачном слое
		var hudR image.Rectangle
		if len(hudTracks) > 0 {
			r1 := hudPaintLegend(layer, hudTracks, hudScale)
			r2 := image.Rectangle{}
			if hasTime {
				r2 = hudPaintSpeeds(layer, hudTracks, tNow, "kmh", hudScale)
			}
			hudR = r1
			if !r2.Empty() {
				if hudR.Empty() {
					hudR = r2
				} else {
					hudR = hudR.Union(r2)
				}
			}
			if !hudR.Empty() {
				if dirty.Empty() {
					dirty = hudR
				} else {
					dirty = dirty.Union(hudR)
				}
			}
		}

		if !drew && hudR.Empty() {
			one := image.NewRGBA(image.Rect(0, 0, 1, 1))
			p := toPalettedTransparent(one, pal)
			frames = append(frames, &PalFrame{Img: p, Rect: p.Rect})
			delays = append(delays, maxInt(delayCS, 1))
			continue
		}

		dirty = dirty.Intersect(layer.Bounds())
		if dirty.Empty() {
			dirty = image.Rect(0, 0, 1, 1)
		}

		sub := layer.SubImage(dirty)
		pimg := toPalettedTransparent(sub, pal)
		frames = append(frames, &PalFrame{Img: pimg, Rect: dirty})
		delays = append(delays, maxInt(delayCS, 1))
	}

	return frames, delays, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
