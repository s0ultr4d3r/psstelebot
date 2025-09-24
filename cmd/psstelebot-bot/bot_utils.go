package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Конвертация GIF → MP4 с той же визуальной скоростью.
// 1) Разворачиваем частичные кадры GIF в полноразмерные PNG с учётом Disposal.
// 2) Выбираем базовый FPS по МОДЕ задержек (в центисекундах), а не по НОД.
// 3) Дублируем каждый PNG столько раз, чтобы выдержать его задержку при CFR.
// 4) Кодируем через image2: -framerate <fps> -i seq_%06d.png -r <fps> -fps_mode cfr.
func gifToMP4SameTiming(ctx context.Context, gifPath string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH")
	}

	// читаем GIF
	gf, err := os.Open(gifPath)
	if err != nil {
		return "", err
	}
	defer gf.Close()

	g, err := gif.DecodeAll(gf)
	if err != nil {
		return "", fmt.Errorf("decode gif: %w", err)
	}
	if len(g.Image) == 0 {
		return "", fmt.Errorf("gif has no frames")
	}

	W, H := g.Config.Width, g.Config.Height
	fullRect := image.Rect(0, 0, W, H)

	// фон (для DisposalBackground)
	var bg color.Color = color.RGBA{0, 0, 0, 0}
	if pal, ok := g.Config.ColorModel.(color.Palette); ok {
		if int(g.BackgroundIndex) < len(pal) {
			bg = pal[g.BackgroundIndex]
		}
	}

	// рабочая папка
	work, err := os.MkdirTemp("", "pss_mp4_*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()

	// разворачиваем частичные кадры в полноразмерные PNG
	canvas := image.NewRGBA(fullRect)
	clearRect(canvas, fullRect, bg)

	n := len(g.Image)
	framePNG := make([]string, n)
	delaysCS := make([]int, n)

	var prev *image.RGBA
	for i := 0; i < n; i++ {
		frame := g.Image[i] // *image.Paletted
		fr := frame.Bounds()

		// disposal для ТЕКУЩЕГО кадра
		dis := byte(gif.DisposalNone)
		if i < len(g.Disposal) {
			dis = g.Disposal[i]
		}

		// снимок для DisposalPrevious
		if dis == byte(gif.DisposalPrevious) {
			prev = cloneRGBA(canvas)
		} else {
			prev = nil
		}

		// рисуем частичный кадр
		draw.Draw(canvas, fr, frame, fr.Min, draw.Over)

		// сохраняем полный PNG текущего состояния
		fn := filepath.Join(work, fmt.Sprintf("frame_%05d.png", i))
		if err := savePNG(fn, canvas); err != nil {
			return "", err
		}
		framePNG[i] = fn

		// нормализуем задержку (в cs — 1/100 s)
		cs := 10 // дефолт 0.10s
		if i < len(g.Delay) && g.Delay[i] > 0 {
			cs = g.Delay[i]
		}
		if cs < 1 {
			cs = 1
		}
		delaysCS[i] = cs

		// подготавливаем холст к СЛЕДУЮЩЕМУ кадру в соответствии с disposal
		switch dis {
		case byte(gif.DisposalNone):
			// оставляем как есть
		case byte(gif.DisposalBackground):
			clearRect(canvas, fr, bg)
		case byte(gif.DisposalPrevious):
			if prev != nil {
				draw.Draw(canvas, fullRect, prev, image.Point{}, draw.Src)
			}
		default:
			// трактуем как None
		}
	}

	// базовый шаг: МОДА задержек (самое частое значение), а не НОД
	modeCS := mostCommonCS(delaysCS)
	if modeCS < 1 {
		modeCS = 5 // страховка
	}
	baseFPS := 100 / modeCS
	if baseFPS < 1 {
		baseFPS = 1
	}
	if baseFPS > 60 {
		baseFPS = 60
	}

	// готовим реальную последовательность кадров под CFR baseFPS
	seqDir := filepath.Join(work, "seq")
	if err := os.MkdirAll(seqDir, 0o755); err != nil {
		return "", err
	}

	writeCopy := func(src, dst string) error {
		// быстрый путь — хардлинк
		if err := os.Link(src, dst); err == nil {
			return nil
		}
		// иначе — быстрая копия
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return out.Sync()
	}

	index := 0
	for i := 0; i < n; i++ {
		// сколько «базовых шагов» в задержке этого кадра
		reps := int(math.Round(float64(delaysCS[i]) / float64(modeCS)))
		if reps < 1 {
			reps = 1
		}
		for r := 0; r < reps; r++ {
			dst := filepath.Join(seqDir, fmt.Sprintf("seq_%06d.png", index))
			if err := writeCopy(framePNG[i], dst); err != nil {
				return "", err
			}
			index++
		}
	}
	if index == 0 {
		return "", fmt.Errorf("no frames prepared for mp4")
	}

	// ffmpeg: image2 → CFR baseFPS (Telegram больше не ускорит)
	out := filepath.Join(filepath.Dir(gifPath), trimExt(filepath.Base(gifPath))+".mp4")

	args := []string{
		"-y",
		"-framerate", fmt.Sprintf("%d", baseFPS),            // входной fps
		"-i", filepath.Join(seqDir, "seq_%06d.png"),        // image2
		"-fps_mode", "cfr",
		"-r", fmt.Sprintf("%d", baseFPS),                   // выходной CFR
		"-movflags", "faststart",
		"-an",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		out,
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cctx, "ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg: %w", err)
	}

	return out, nil
}

/*************** helpers ***************/

func mostCommonCS(a []int) int {
	if len(a) == 0 {
		return 5
	}
	// округлим все экстремально маленькие/нулевые значения вверх до 1
	cnt := map[int]int{}
	for _, v := range a {
		if v < 1 {
			v = 1
		}
		cnt[v]++
	}
	// находим моду; при равенстве — берём наибольшую (обычно 5)
	bestV, bestC := 5, -1
	for v, c := range cnt {
		if c > bestC || (c == bestC && v > bestV) {
			bestV, bestC = v, c
		}
	}
	return bestV
}

func clearRect(dst *image.RGBA, r image.Rectangle, c color.Color) {
	cr, cg, cb, ca := c.RGBA()
	px := color.NRGBA{R: uint8(cr >> 8), G: uint8(cg >> 8), B: uint8(cb >> 8), A: uint8(ca >> 8)}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := (y-dst.Rect.Min.Y)*dst.Stride + (r.Min.X-dst.Rect.Min.X)*4
		for x := r.Min.X; x < r.Max.X; x++ {
			dst.Pix[i+0] = px.R
			dst.Pix[i+1] = px.G
			dst.Pix[i+2] = px.B
			dst.Pix[i+3] = px.A
			i += 4
		}
	}
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	cp := image.NewRGBA(src.Bounds())
	draw.Draw(cp, cp.Bounds(), src, src.Bounds().Min, draw.Src)
	return cp
}

func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func trimExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func sendChatAction(bot *tgbotapi.BotAPI, chatID int64, action string) error {
	act := tgbotapi.NewChatAction(chatID, action)
	_, err := bot.Send(act)
	return err
}
