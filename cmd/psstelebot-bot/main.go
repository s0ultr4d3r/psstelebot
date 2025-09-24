package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

/*************** Конфиг ****************/

var rendererPath = "./bin/psstelebot"

var defaultRender = renderConfig{
	Size:           1024,
	FPS:            20,
	Duration:       "15s",
	TilesPreset:    "opentopomap",
	TilesURL:       "",
	TilesMaxZoom:   16,
	TilesMinZoom:   12,
	TilesRetries:   3,
	TilesRetryBack: "2s",
	TilesTimeout:   "45s",
	TilesRPS:       0.5,
	TilesBurst:     1,
	TileFit:        "cover",
	Margin:         0.02,
	LineColors:     "#e74c3c,#3498db",
	SpeedOverlay:   true,
	SpeedUnits:     "kmh",
	SpeedSmooth:    1,
	LineWidth:      4,
	GlobalTimeout:  "60m",
	ShowLegend:     true,
}

type renderConfig struct {
	Size           int
	FPS            int
	Duration       string
	TilesPreset    string
	TilesURL       string
	TilesMaxZoom   int
	TilesMinZoom   int
	TilesRetries   int
	TilesRetryBack string
	TilesTimeout   string
	TilesRPS       float64
	TilesBurst     int
	TileFit        string
	Margin         float64
	LineColors     string
	SpeedOverlay   bool
	SpeedUnits     string
	SpeedSmooth    int
	LineWidth      int
	GlobalTimeout  string
	ShowLegend     bool
}

func (c renderConfig) args(out string, inputs []string) []string {
	args := []string{
		"-size", strconv.Itoa(c.Size),
		"-fps", strconv.Itoa(c.FPS),
		"-duration", c.Duration,
		"-tilesMaxZoom", strconv.Itoa(c.TilesMaxZoom),
		"-tilesMinZoom", strconv.Itoa(c.TilesMinZoom),
		"-tilesRetries", strconv.Itoa(c.TilesRetries),
		"-tilesRetryBackoff", c.TilesRetryBack,
		"-tilesTimeout", c.TilesTimeout,
		"-tilesRPS", fmt.Sprintf("%g", c.TilesRPS),
		"-tilesBurst", strconv.Itoa(c.TilesBurst),
		"-tileFit", c.TileFit,
		"-margin", fmt.Sprintf("%g", c.Margin),
		"-lineColors", c.LineColors,
		"-speedUnits", c.SpeedUnits,
		"-speedSmooth", strconv.Itoa(c.SpeedSmooth),
		"-lineWidth", strconv.Itoa(c.LineWidth),
		"-timeout", c.GlobalTimeout,
	}
	if c.TilesURL != "" {
		args = append(args, "-tilesURL", c.TilesURL)
	} else {
		args = append(args, "-tilesPreset", c.TilesPreset)
	}
	if c.SpeedOverlay {
		args = append(args, "-speedOverlay")
	}
	if c.ShowLegend {
		args = append(args, "-legend")
	}
	for _, in := range inputs {
		args = append(args, "-in", in)
	}
	args = append(args, "-out", out)
	return args
}

/*************** Состояние на чат ****************/

var (
	build   = "dev"
	mu      sync.Mutex
	perChat = map[int64]*chatState{}
)

type chatState struct {
	cfg   renderConfig
	files []string
}

func state(chatID int64) *chatState {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := perChat[chatID]; ok {
		return s
	}
	cp := defaultRender
	s := &chatState{cfg: cp}
	perChat[chatID] = s
	return s
}

func (s *chatState) enqueue(path string) {
	mu.Lock()
	defer mu.Unlock()
	s.files = append(s.files, path)
}

func (s *chatState) clearFiles() (cleared []string) {
	mu.Lock()
	defer mu.Unlock()
	cleared = s.files
	s.files = nil
	return
}

/*************** Клавиатура (ReplyKeyboard) ****************/

const (
	btnTopoText = "🗺️ Топографическая карта"
	btnSatText  = "🛰️ Спутниковая карта"
)

func bottomKeyboard() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnTopoText),
			tgbotapi.NewKeyboardButton(btnSatText),
		),
	)
}

/*************** Бот ****************/

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN is empty")
	}
	if _, err := os.Stat(rendererPath); err != nil {
		log.Fatalf("renderer not found: %s", rendererPath)
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("psstelebot-bot build=%s authorized on %s", build, bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.Message != nil {
			go handleMessage(bot, upd.Message)
		}
	}
}

/*************** Handlers ****************/

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	st := state(chatID)

	// Команды
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			txt := "Пришлите один или несколько GPX *как документы* (можно альбомом). " +
				"Когда будете готовы — нажмите кнопку внизу: «Топографическая карта» или «Спутниковая карта» — это и выберет карту, и запустит рендер.\n\n" +
				"Все параметры — в /help"
			m := tgbotapi.NewMessage(chatID, txt)
			m.ParseMode = "Markdown"
			kbd := bottomKeyboard()
			kbd.ResizeKeyboard = true
			m.ReplyMarkup = kbd
			_, _ = bot.Send(m)
			return

		case "help":
			_ = sendText(bot, chatID,
				"*Опции:*\n"+
					"`/size 1536`, `/fps 20`, `/duration 12s`\n"+
					"`/linewidth 4`, `/colors #e74c3c,#3498db`\n"+
					"`/legend on|off`, `/speed on|off`\n"+
					"`/list` — показать очередь, `/clear` — очистить.\n\n"+
					"Кнопки внизу запускают отрисовку и выбирают карту.",
				true)
			return

		case "list":
			mu.Lock()
			files := append([]string(nil), st.files...)
			mu.Unlock()
			if len(files) == 0 {
				_ = sendText(bot, chatID, "Очередь пуста.", false)
			} else {
				var b strings.Builder
				for _, f := range files {
					b.WriteString("• ")
					b.WriteString(filepath.Base(f))
					b.WriteByte('\n')
				}
				_ = sendText(bot, chatID, "*В очереди:*\n"+b.String(), true)
			}
			return

		case "clear":
			toDel := st.clearFiles()
			cleanTemp(toDel)
			_ = sendText(bot, chatID, "Очередь очищена.", false)
			return

		case "size":
			if v, err := strconv.Atoi(strings.TrimSpace(msg.CommandArguments())); err == nil && v >= 256 && v <= 4096 {
				st.cfg.Size = v
				_ = sendText(bot, chatID, fmt.Sprintf("size=%d", v), false)
			}
			return

		case "fps":
			if v, err := strconv.Atoi(strings.TrimSpace(msg.CommandArguments())); err == nil && v >= 1 && v <= 60 {
				st.cfg.FPS = v
				_ = sendText(bot, chatID, fmt.Sprintf("fps=%d", v), false)
			}
			return

		case "duration":
			a := strings.TrimSpace(msg.CommandArguments())
			if a != "" {
				st.cfg.Duration = a
				_ = sendText(bot, chatID, "duration="+a, false)
			}
			return

		case "linewidth":
			if v, err := strconv.Atoi(strings.TrimSpace(msg.CommandArguments())); err == nil && v >= 1 && v <= 20 {
				st.cfg.LineWidth = v
				_ = sendText(bot, chatID, fmt.Sprintf("lineWidth=%d", v), false)
			}
			return

		case "colors":
			a := strings.TrimSpace(msg.CommandArguments())
			if a != "" {
				st.cfg.LineColors = a
				_ = sendText(bot, chatID, "colors="+a, false)
			}
			return

		case "legend":
			a := strings.TrimSpace(msg.CommandArguments())
			if a == "on" {
				st.cfg.ShowLegend = true
			} else if a == "off" {
				st.cfg.ShowLegend = false
			}
			_ = sendText(bot, chatID, fmt.Sprintf("legend=%v", st.cfg.ShowLegend), false)
			return

		case "speed":
			a := strings.TrimSpace(msg.CommandArguments())
			if a == "on" {
				st.cfg.SpeedOverlay = true
			} else if a == "off" {
				st.cfg.SpeedOverlay = false
			}
			_ = sendText(bot, chatID, fmt.Sprintf("speedOverlay=%v", st.cfg.SpeedOverlay), false)
			return
		}
	}

	// Нажатия кнопок нижней клавиатуры (обычный текст)
	switch msg.Text {
	case btnTopoText:
		st.cfg.TilesPreset = "opentopomap"
		st.cfg.TilesURL = ""
		runRenderNow(bot, chatID)
		return
	case btnSatText:
		st.cfg.TilesPreset = "esri-satellite"
		st.cfg.TilesURL = ""
		runRenderNow(bot, chatID)
		return
	}

	// Документ .gpx — только добавляем в очередь, без автозапуска
	if msg.Document != nil && msg.Document.FileID != "" {
		if !strings.HasSuffix(strings.ToLower(msg.Document.FileName), ".gpx") {
			_ = sendText(bot, chatID, "Это не .gpx. Пришлите GPX как документ.", false)
			return
		}
		path := maybeDownload(bot, msg.Document)
		if path != "" {
			st.enqueue(path)
			mu.Lock()
			n := len(st.files)
			mu.Unlock()
			txt := fmt.Sprintf("Добавлен: *%s*\nВ очереди теперь %d файл(ов). Нажмите кнопку внизу для старта.",
				filepath.Base(path), n)
			m := tgbotapi.NewMessage(chatID, txt)
			m.ParseMode = "Markdown"
			// клавиатуру снизу держим постоянно — не нужно добавлять её к каждому сообщению
			_, _ = bot.Send(m)
		} else {
			_ = sendText(bot, chatID, "Не удалось скачать файл.", false)
		}
	}
}

/*************** Рендер ****************/

func runRenderNow(bot *tgbotapi.BotAPI, chatID int64) {
	st := state(chatID)

	mu.Lock()
	files := append([]string(nil), st.files...)
	cfg := st.cfg
	mu.Unlock()

	if len(files) == 0 {
		_ = sendText(bot, chatID, "Очередь пуста. Пришлите один или несколько GPX.", false)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	_ = sendChatAction(bot, chatID, "typing")
	outGIF, err := renderWithConfig(ctx, files, &cfg)
	// очищаем очередь
	cleanTemp(st.clearFiles())

	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Ошибка рендера: "+err.Error()))
		return
	}
	defer os.Remove(outGIF)

	if err := sendResult(ctx, bot, chatID, outGIF,
		fmt.Sprintf("Готово ✅ (%d GPX) · карта=%s", len(files), cfg.TilesPreset)); err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Ошибка отправки: "+err.Error()))
	}
}

/*************** Скачивание / утилиты ****************/

func maybeDownload(bot *tgbotapi.BotAPI, d *tgbotapi.Document) string {
	if d == nil {
		return ""
	}
	name := d.FileName
	if name == "" {
		name = "file.gpx"
	}
	f, err := bot.GetFile(tgbotapi.FileConfig{FileID: d.FileID})
	if err != nil {
		return ""
	}
	url := f.Link(bot.Token)

	tmpDir := filepath.Join(os.TempDir(), "pssbot")
	_ = os.MkdirAll(tmpDir, 0o755)
	dst := filepath.Join(tmpDir, sanitize(name))
	out, err := os.Create(dst)
	if err != nil {
		return ""
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		_ = os.Remove(dst)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_ = os.Remove(dst)
		return ""
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = os.Remove(dst)
		return ""
	}
	return dst
}

func sanitize(name string) string {
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

func cleanTemp(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func renderWithConfig(ctx context.Context, gpx []string, cfg *renderConfig) (string, error) {
	if len(gpx) == 0 {
		return "", errors.New("no gpx")
	}
	tmpDir := filepath.Join(os.TempDir(), "pssbot")
	_ = os.MkdirAll(tmpDir, 0o755)
	out := filepath.Join(tmpDir, fmt.Sprintf("track_%d.gif", time.Now().UnixNano()))

	args := cfg.args(out, gpx)
	cmd := exec.CommandContext(ctx, rendererPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("run: %s %s", rendererPath, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return "", fmt.Errorf("renderer output not found: %s", out)
	}
	return out, nil
}

/*************** Telegram helpers ****************/

func sendText(bot *tgbotapi.BotAPI, chatID int64, text string, markdown bool) error {
	msg := tgbotapi.NewMessage(chatID, text)
	if markdown {
		msg.ParseMode = "Markdown"
	}
	_, err := bot.Send(msg)
	return err
}
