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
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Конвертация GIF → MP4 с точными задержками (VFR) + настраиваемый коэффициент скорости.
// PSS_MP4_SPEED (float, по умолчанию 1.0) умножает все duration ( >1.0 = медленнее, <1.0 = быстрее).
func gifToMP4SameTiming(ctx context.Context, gifPath string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH")
	}

	// 1) читаем GIF
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

	// 2) рабочая папка
	work, err := os.MkdirTemp("", "pss_mp4_*")
	if err != nil {
		return "", err
	}
	// подчистим после
	defer func() { _ = os.RemoveAll(work) }()

	// 3) разворачиваем частичные кадры в полноразмерные PNG
	canvas := image.NewRGBA(fullRect)
	clearRect(canvas, fullRect, bg)

	n := len(g.Image)
	framePNG := make([]string, n)
	delaySec := make([]float64, n) // задержки в секундах

	var prev *image.RGBA
	totalGIF := 0.0
	for i := 0; i < n; i++ {
		frame := g.Image[i] // *image.Paletted
		fr := frame.Bounds()

		// disposal для текущего кадра (byte)
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

		// рисуем частичный кадр на холст
		draw.Draw(canvas, fr, frame, fr.Min, draw.Over)

		// сохраняем полный PNG
		fn := filepath.Join(work, fmt.Sprintf("frame_%05d.png", i))
		if err := savePNG(fn, canvas); err != nil {
			return "", err
		}
		framePNG[i] = fn

		// задержка кадра из GIF → секунды (Delay — в 1/100 s); нули нормализуем
		cs := 10 // дефолт 0.10s
		if i < len(g.Delay) && g.Delay[i] > 0 {
			cs = g.Delay[i]
		}
		if cs < 1 {
			cs = 1
		}
		sec := float64(cs) / 100.0
		delaySec[i] = sec
		totalGIF += sec

		// подготовка к следующему кадру по disposal
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

	// 4) коэффициент скорости (>=0.2 .. <=5.0), по умолчанию 1.0
	speed := readSpeedFactor()
	if math.Abs(speed-1.0) > 1e-9 {
		for i := range delaySec {
			delaySec[i] *= speed
		}
	}

	// 5) формируем ffconcat-список (VFR) — file + duration для каждого кадра и file для последнего
	listPath := filepath.Join(work, "list.txt")
	lst, err := os.Create(listPath)
	if err != nil {
		return "", err
	}
	_, _ = io.WriteString(lst, "ffconcat version 1.0\n")
	totalMP4 := 0.0
	for i := 0; i < n; i++ {
		abs, _ := filepath.Abs(framePNG[i])
		_, _ = io.WriteString(lst, fmt.Sprintf("file '%s'\n", escapeFF(abs)))
		d := delaySec[i]
		if d < 0.005 {
			d = 0.005 // минимальная разумная длительность, чтобы телега не «съела» кадр
		}
		_, _ = io.WriteString(lst, fmt.Sprintf("duration %.6f\n", d))
		totalMP4 += d
	}
	// повторяем последний файл без duration
	if n > 0 {
		abs, _ := filepath.Abs(framePNG[n-1])
		_, _ = io.WriteString(lst, fmt.Sprintf("file '%s'\n", escapeFF(abs)))
	}
	_ = lst.Close()

	// 6) ffmpeg: concat (VFR) → MP4
	out := filepath.Join(filepath.Dir(gifPath), trimExt(filepath.Base(gifPath))+".mp4")
	args := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-fflags", "+genpts",    // генерируем PTS по duration
		"-vsync", "vfr",         // переменный FPS, уважаем duration
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-movflags", "faststart",
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

	// необязательный лог в консоль, чтобы видеть расчетные длительности
	fmt.Fprintf(os.Stderr, "[mp4] GIF dur=%.3fs, MP4 dur (target)=%.3fs, speed=%.3fx\n", totalGIF, totalMP4, speed)

	return out, nil
}

/*************** helpers ***************/

func readSpeedFactor() float64 {
	s := strings.TrimSpace(os.Getenv("PSS_MP4_SPEED"))
	if s == "" {
		return 1.0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !isFinite(v) {
		return 1.0
	}
	if v < 0.2 {
		v = 0.2
	}
	if v > 5.0 {
		v = 5.0
	}
	return v
}

func isFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
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

func escapeFF(p string) string {
	// экранируем одинарные кавычки для ffconcat
	return strings.ReplaceAll(p, "'", "'\\''")
}

func sendChatAction(bot *tgbotapi.BotAPI, chatID int64, action string) error {
	act := tgbotapi.NewChatAction(chatID, action)
	_, err := bot.Send(act)
	return err
}
