package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	token := mustEnv("BOT_TOKEN")
	renderBin := mustEnv("PSSTELE_BIN") // путь к собранному cmd/gpx2gif
	workDir := getenv("WORK_DIR", "./work")
	_ = os.MkdirAll(workDir, 0o755)

	bot, err := tgbotapi.NewBotAPI(token)
	must(err)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		// Простейшая логика: прислали .gpx — скачиваем и рендерим с дефолтами
		if d := update.Message.Document; d != nil && strings.HasSuffix(strings.ToLower(d.FileName), ".gpx") {
			dst := filepath.Join(workDir, filepath.Base(d.FileName))
			if err := downloadFile(bot, d.FileID, dst); err != nil {
				send(bot, chatID, "❌ "+err.Error()); continue
			}
			send(bot, chatID, "✅ GPX получен, рендерю…")

			out := filepath.Join(workDir, time.Now().Format("20060102_150405") + ".gif")
			args := []string{"-in", dst, "-out", out, "-size", "1024", "-fps", "20", "-duration", "12s"}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, renderBin, args...)
			cmd.Env = append(os.Environ(),
				"STADIA_KEY="+os.Getenv("STADIA_KEY"),
				"MAPTILER_KEY="+os.Getenv("MAPTILER_KEY"),
			)

			if outb, err := cmd.CombinedOutput(); err != nil {
				send(bot, chatID, "❌ Ошибка рендера:\n"+tail(string(outb), 40))
				continue
			}

			_, _ = bot.Send(tgbotapi.NewAnimation(chatID, tgbotapi.FilePath(out)))
			_ = os.Remove(out)
			_ = os.Remove(dst)
			continue
		}

		if update.Message.IsCommand() && update.Message.Command() == "render" {
			send(bot, chatID, "⚙️ Пришли .gpx документом — отрендерю с дефолтами. Опции добавим дальше.")
		}
	}
}

func downloadFile(bot *tgbotapi.BotAPI, fileID, dst string) error {
	fc, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil { return err }
	// без внешних зависимостей: используем curl
	if out, err := exec.Command("curl", "-fsSL", fc.Link(bot.Token), "-o", dst).CombinedOutput(); err != nil {
		return fmt.Errorf("curl: %v: %s", err, string(out))
	}
	return nil
}
func send(bot *tgbotapi.BotAPI, chatID int64, text string) { _, _ = bot.Send(tgbotapi.NewMessage(chatID, text)) }
func tail(s string, n int) string { ls := strings.Split(s, "\n"); if len(ls) <= n { return s }; return strings.Join(ls[len(ls)-n:], "\n") }
func must(err error) { if err != nil { panic(err) } }
func mustEnv(k string) string { v := os.Getenv(k); if v == "" { panic("missing " + k) }; return v }
func getenv(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }
