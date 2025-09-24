package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// sendResult: шлёт ZIP (оригинальный GIF) и MP4 (с теми же таймингами, что у GIF).
// Конвертация в MP4 делается через gifToMP4SameTiming (реализация в bot_utils.go).
func sendResult(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, gifPath string, caption string) error {
	zipPath, err := makeZipWithSingleFile(gifPath)
	if err != nil {
		return fmt.Errorf("zip gif: %w", err)
	}
	defer func() { _ = os.Remove(zipPath) }()

	// 1) ZIP как документ — телега не пережимает
	if err := sendDocument(bot, chatID, zipPath, caption+" (ZIP с оригинальным GIF)"); err != nil {
		return fmt.Errorf("send zip: %w", err)
	}

	// 2) MP4 с теми же таймингами, что у GIF
	mp4Path, err := gifToMP4SameTiming(ctx, gifPath)
	if err != nil {
		// ffmpeg может отсутствовать — просто логгируем и не считаем фаталом
		fmt.Fprintf(os.Stderr, "[warn] convert to mp4: %v\n", err)
		return nil
	}
	defer func() { _ = os.Remove(mp4Path) }()

	if err := sendVideo(bot, chatID, mp4Path, "MP4-версия (та же скорость, что у GIF)"); err != nil {
		return fmt.Errorf("send mp4: %w", err)
	}
	return nil
}

func sendDocument(bot *tgbotapi.BotAPI, chatID int64, path string, caption string) error {
	_ = sendChatAction(bot, chatID, "upload_document") // функция определена в main.go
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
	doc.DisableContentTypeDetection = true
	if caption != "" {
		doc.Caption = caption
	}
	_, err := bot.Send(doc)
	return err
}

func sendVideo(bot *tgbotapi.BotAPI, chatID int64, mp4Path string, caption string) error {
	_ = sendChatAction(bot, chatID, "upload_video") // функция определена в main.go
	msg := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(mp4Path))
	if caption != "" {
		msg.Caption = caption
	}
	_, err := bot.Send(msg)
	return err
}

// makeZipWithSingleFile упаковывает один файл в zip (рядом с исходником) и возвращает путь к zip.
func makeZipWithSingleFile(srcPath string) (string, error) {
	base := filepath.Base(srcPath)
	zipName := trimExt(base) + ".zip" // trimExt определён в bot_utils.go
	dst := filepath.Join(filepath.Dir(srcPath), zipName)

	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()

	w := zip.NewWriter(out)
	defer w.Close()

	fw, err := w.Create(base)
	if err != nil {
		return "", err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer in.Close()

	if _, err := io.Copy(fw, in); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	return dst, nil
}
