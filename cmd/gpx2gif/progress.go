package main

import (
	"fmt"
	"sync"
)

type Bars struct {
	mu     sync.Mutex
	totalG int
	doneG  int

	GIF *Bar
}

type Bar struct {
	mu     sync.Mutex
	total  int
	done   int
	prefix string
}

func NewBars(totalGPX, totalGIF int) *Bars {
	b := &Bars{}
	b.totalG = totalGPX
	b.GIF = &Bar{total: totalGIF, prefix: "[GIF]"}
	fmt.Printf("[GPX] обработка 0%% [%-40s] (0/%d)", "", totalGPX)
	return b
}

func (b *Bars) Done() {
	fmt.Println()
}

func (b *Bars) IncGPX() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.doneG++
	perc := 0
	if b.totalG > 0 {
		perc = b.doneG * 100 / b.totalG
	}
	fill := perc * 40 / 100
	fmt.Printf("\r[GPX] обработка %d%% [%-40s] (%d/%d)", perc, progressBar(fill), b.doneG, b.totalG)
}

func (b *Bars) SetGIF(n int) {
	b.GIF.Set(n)
}

func (bar *Bar) ChangeMax(n int) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	bar.total = n
}

func (bar *Bar) Set(n int) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	bar.done = n
	perc := 0
	if bar.total > 0 {
		perc = bar.done * 100 / bar.total
	}
	fill := perc * 40 / 100
	fmt.Printf("\r%s %d%% [%-40s] (%d/%d)", bar.prefix, perc, progressBar(fill), bar.done, bar.total)
}

func progressBar(fill int) string {
	if fill < 0 {
		fill = 0
	}
	if fill > 40 {
		fill = 40
	}
	return fmt.Sprintf("%s", string([]rune("========================================")[:fill]))
}
