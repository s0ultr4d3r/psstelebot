package main

import (
	"time"

	"github.com/schollz/progressbar/v3"
)

type Bars struct {
	GPX *progressbar.ProgressBar
	GIF *progressbar.ProgressBar
}

func NewBars(totalGPX, totalGIF int) *Bars {
	theme := progressbar.Theme{
		Saucer:        "=",
		SaucerHead:    ">",
		SaucerPadding: " ",
		BarStart:      "[",
		BarEnd:        "]",
	}
	gpx := progressbar.NewOptions(totalGPX,
		progressbar.OptionSetTheme(theme),
		progressbar.OptionSetDescription("[GPX] обработка"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionThrottle(100*time.Millisecond),
	)
	gif := progressbar.NewOptions(totalGIF,
		progressbar.OptionSetTheme(theme),
		progressbar.OptionSetDescription("[GIF] конвертация"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionThrottle(100*time.Millisecond),
	)
	return &Bars{GPX: gpx, GIF: gif}
}

func (b *Bars) SetGPX(i int) { _ = b.GPX.Set(i) }
func (b *Bars) SetGIF(i int) { _ = b.GIF.Set(i) }
func (b *Bars) IncGPX()      { _ = b.GPX.Add(1) }
func (b *Bars) IncGIF()      { _ = b.GIF.Add(1) }

func (b *Bars) Done() {
	_ = b.GPX.Finish()
	_ = b.GIF.Finish()
}
