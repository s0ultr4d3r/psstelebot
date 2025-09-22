package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
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

type Session struct {
	Files      []string
	UpdatedAt  time.Time
	LastUserID int64  // кто вызывал команду
	LastUser   string // username (без @), если есть
}

var (
	bot         *tgbotapi.BotAPI
	pssBin      string
	workDir     string
	sessionTTL  = 45 * time.Minute
	sessions    = map[int64]*Session{} // chatID -> session
	sessMu      sync.Mutex
	renderSlots = make(chan struct{}, 2) // одновременно не более 2 рендеров

	debugAdmins = map[int64]bool{}  // по userID
	debugUsers  = map[string]bool{} // по username (lower)

	mainKB tgbotapi.ReplyKeyboardMarkup
)

const (
	maxBotUpload = 48 * 1024 * 1024 // ~48MB запас к лимиту Telegram (50MB)

	btnTop = "🗺️ Топографическая карта"
	btnSat = "🛰️ Спутниковая карта"
)

// ---------- MAIN ----------

func main() {
	token := mustEnv("BOT_TOKEN")
	pssBin = mustEnv("PSSTELE_BIN")
	workDir = getenv("WORK_DIR", "./.work")
	_ = os.MkdirAll(workDir, 0o755)

	// DEBUG_ADMINS="12345,@user1,67890,@user2"
	parseDebugAdmins(getenv("DEBUG_ADMINS", ""))

	// reply-клавиатура
	mainKB = tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnTop),
			tgbotapi.NewKeyboardButton(btnSat),
		),
	)
	mainKB.ResizeKeyboard = true
	mainKB.OneTimeKeyboard = false
	mainKB.Selective = false

	// HTTP-клиент без keep-alive/HTTP2 для стабильного long-polling
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   20 * time.Second,
			KeepAlive: 20 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
		ForceAttemptHTTP2:   false,
	}
	httpClient := &http.Client{Transport: tr}

	var err error
	bot, err = tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient)
	must(err)

	go gcSessions()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 50
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.Message == nil {
			continue
		}
		m := upd.Message
		chatID := m.Chat.ID

		// Команды
		if m.IsCommand() {
			switch m.Command() {
			case "start", "help":
				sendKB(chatID, helpText())
			case "clear":
				clearSession(chatID)
				sendKB(chatID, "🧼 Сессия очищена. Пришлите 1..N GPX, затем выберите кнопку ниже.")
			case "rendertop":
				markInvoker(chatID, m)
				doRender(chatID, "opentopomap", m)
			case "rendersat":
				markInvoker(chatID, m)
				doRender(chatID, "esri-satellite", m)
			default:
				sendKB(chatID, "Неизвестная команда. Нажмите одну из кнопок ниже или /help.")
			}
			continue
		}

		// Нажатия на кнопки (приходят как обычный текст)
		if m.Text == btnTop {
			markInvoker(chatID, m)
			doRender(chatID, "opentopomap", m)
			continue
		}
		if m.Text == btnSat {
			markInvoker(chatID, m)
			doRender(chatID, "esri-satellite", m)
			continue
		}

		// Приём GPX
		if d := m.Document; d != nil {
			name := strings.ToLower(d.FileName)
			if !strings.HasSuffix(name, ".gpx") {
				sendKB(chatID, "📄 Это не GPX. Пришлите .gpx файл(ы) или нажмите кнопку ниже.")
				continue
			}
			dst := filepath.Join(workDir, fmt.Sprintf("%d_%s", chatID, sanitize(d.FileName)))
			if err := downloadTGFile(d.FileID, dst); err != nil {
				sendKB(chatID, "❌ Не удалось скачать файл: "+err.Error())
				continue
			}
			addFile(chatID, dst)
			sendKB(chatID, fmt.Sprintf("✅ GPX добавлен: %s\nКогда готовы — нажмите кнопку ниже:", filepath.Base(dst)))
			continue
		}
	}
}

// ---------- RENDER ----------

func doRender(chatID int64, tilesPreset string, m *tgbotapi.Message) {
	sessMu.Lock()
	s := sessions[chatID]
	sessMu.Unlock()

	if s == nil || len(s.Files) == 0 {
		sendKB(chatID, "⚠️ В сессии нет GPX. Пришлите 1..N файлов, затем нажмите кнопку ниже.")
		return
	}

	send(chatID, fmt.Sprintf("🛠️ Готовлюсь к рендеру… (preset: %s), файлов: %d", tilesPreset, len(s.Files)))
	dbg := newDebugStreamer(chatID, m)

	// фикс-параметры
	outPath := filepath.Join(workDir, time.Now().Format("20060102_150405")+".gif")
	args := []string{
		"-out", outPath,
		"-size", "1024",
		"-fps", "20",
		"-duration", "15s",
		"-tilesMaxZoom", "16",
		"-tilesMinZoom", "12",
		"-tilesRetries", "3",
		"-tilesRetryBackoff", "2s",
		"-tilesTimeout", "45s",
		"-tilesRPS", "0.5",
		"-tilesBurst", "1",
		"-tileFit", "cover",
		"-margin", "0.02",
		"-lineColors", "#e74c3c,#3498db",
		"-speedOverlay",
		"-speedUnits", "kmh",
		"-speedSmooth", "1",
		"-lineWidth", "3",
		"-timeout", "60m",
		"-tilesPreset", tilesPreset,
	}
	for _, f := range s.Files {
		args = append(args, "-in", f)
	}

	dbg.Println("CMD:", pssBin, strings.Join(args, " "))

	// ограничение параллельности
	renderSlots <- struct{}{}
	defer func() { <-renderSlots }()

	// поверх -timeout
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, pssBin, args...)
	cmd.Env = append(os.Environ(),
		"STADIA_KEY="+os.Getenv("STADIA_KEY"),
		"MAPTILER_KEY="+os.Getenv("MAPTILER_KEY"),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, lineTap(func(line string) { dbg.Println("[stdout]", line) }))
	cmd.Stderr = io.MultiWriter(&stderr, lineTap(func(line string) { dbg.Println("[stderr]", line) }))

	start := time.Now()
	if err := cmd.Run(); err != nil {
		dbg.Flush()
		send(chatID, "❌ Ошибка рендера:\n"+tail(stderr.String(), 40))
		return
	}
	dbg.Println("Render completed in", time.Since(start).Round(time.Second).String())
	dbg.Flush()

	// --- Отправка результата --- //
	send(chatID, "📤 Отправляю результат…")

	// Опционально: ужать GIF перед проверкой размера (если установлен gifsicle)
	tryShrinkGIF(outPath)

	gifSize := fileSize(outPath)
	caption := fmt.Sprintf("✅ Готово • %s • %d трек(ов) • %.1f MB",
		tilesPreset, len(s.Files), float64(gifSize)/(1024*1024))

	sent := false

	if gifSize > 0 && gifSize <= maxBotUpload {
		// Небольшие гифки пробуем как анимацию
		anim := tgbotapi.NewAnimation(chatID, tgbotapi.FilePath(outPath))
		anim.Caption = caption
		if _, err := bot.Send(anim); err == nil {
			sent = true
		} else {
			// Попробуем как документ
			doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(outPath))
			doc.Caption = caption
			if _, err2 := bot.Send(doc); err2 == nil {
				sent = true
			} else {
				send(chatID, "⚠️ Отправка GIF не удалась, пробую MP4…")
			}
		}
	}

	if !sent {
		// либо GIF > лимита, либо отправка не прошла — делаем MP4
		mp4Path := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + ".mp4"
		if err := tryTranscodeGIFtoMP4(outPath, mp4Path); err != nil {
			send(chatID, "❌ Не удалось перекодировать в MP4: "+err.Error())
		} else {
			mp4Size := fileSize(mp4Path)
			captionMP4 := fmt.Sprintf("✅ Готово (MP4) • %s • %d трек(ов) • %.1f MB",
				tilesPreset, len(s.Files), float64(mp4Size)/(1024*1024))

			vid := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(mp4Path))
			vid.Caption = captionMP4
			if _, err := bot.Send(vid); err != nil {
				send(chatID, "❌ Не удалось отправить видео: "+err.Error())
			} else {
				sent = true
			}
			_ = os.Remove(mp4Path)
		}
	}

	_ = os.Remove(outPath)
	clearSession(chatID)

	if sent {
		sendKB(chatID, "📦 Готово. Файлы очищены, сессия сброшена. Можете загрузить новые GPX и выбрать карту кнопкой ниже.")
	}
}

// ---------- Sessions ----------

func addFile(chatID int64, path string) {
	sessMu.Lock()
	defer sessMu.Unlock()
	s := sessions[chatID]
	if s == nil {
		s = &Session{}
		sessions[chatID] = s
	}
	s.Files = append(s.Files, path)
	s.UpdatedAt = time.Now()
}

func markInvoker(chatID int64, m *tgbotapi.Message) {
	sessMu.Lock()
	defer sessMu.Unlock()
	s := sessions[chatID]
	if s == nil {
		s = &Session{}
		sessions[chatID] = s
	}
	s.LastUserID = m.From.ID
	s.LastUser = strings.ToLower(m.From.UserName)
	s.UpdatedAt = time.Now()
}

func clearSession(chatID int64) {
	sessMu.Lock()
	defer sessMu.Unlock()
	if s, ok := sessions[chatID]; ok {
		for _, f := range s.Files {
			_ = os.Remove(f)
		}
		delete(sessions, chatID)
	}
}

func gcSessions() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		sessMu.Lock()
		for id, s := range sessions {
			if now.Sub(s.UpdatedAt) > sessionTTL {
				for _, f := range s.Files {
					_ = os.Remove(f)
				}
				delete(sessions, id)
			}
		}
		sessMu.Unlock()
	}
}

// ---------- Debug streamer ----------

type debugStreamer struct {
	enabled bool
	chatID  int64
	buf     bytes.Buffer
	mu      sync.Mutex
	last    time.Time
}

func newDebugStreamer(chatID int64, m *tgbotapi.Message) *debugStreamer {
	ok := isDebugAllowed(m)
	return &debugStreamer{
		enabled: ok,
		chatID:  chatID,
		last:    time.Now(),
	}
}

func (d *debugStreamer) Println(parts ...interface{}) {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintln(&d.buf, fmt.Sprint(parts...))
	// отправляем батч каждые ~2s или если буфер > 1200 символов
	if time.Since(d.last) > 2*time.Second || d.buf.Len() > 1200 {
		d.flushLocked()
	}
}

func (d *debugStreamer) Flush() {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf.Len() > 0 {
		d.flushLocked()
	}
}

func (d *debugStreamer) flushLocked() {
	text := d.buf.String()
	d.buf.Reset()
	d.last = time.Now()
	if text == "" {
		return
	}
	send(d.chatID, "```log\n"+elide(text, 3500)+"\n```") // код-блок для читабельности
}

func isDebugAllowed(m *tgbotapi.Message) bool {
	if m == nil || m.From == nil {
		return false
	}
	if debugAdmins[m.From.ID] {
		return true
	}
	user := strings.ToLower(m.From.UserName)
	if user != "" && debugUsers[user] {
		return true
	}
	return false
}

func parseDebugAdmins(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "@") {
			debugUsers[strings.ToLower(strings.TrimPrefix(tok, "@"))] = true
			continue
		}
		if id, err := strconv.ParseInt(tok, 10, 64); err == nil {
			debugAdmins[id] = true
		}
	}
}

// ---------- Telegram helpers ----------

func send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, _ = bot.Send(msg)
}

func sendKB(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = mainKB
	_, _ = bot.Send(msg)
}

func downloadTGFile(fileID, dst string) error {
	fc, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return err
	}
	resp, err := http.Get(fc.Link(bot.Token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("telegram file HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, resp.Body); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ---------- Utils ----------

func lineTap(cb func(string)) io.Writer {
	pr, pw := io.Pipe()
	go func() {
		r := bufio.NewScanner(pr)
		const max = 1024 * 1024
		r.Buffer(make([]byte, 0, 64*1024), max)
		for r.Scan() {
			cb(r.Text())
		}
	}()
	return pw
}

func elide(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

func tail(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) <= n {
		return s
	}
	return strings.Join(ls[len(ls)-n:], "\n")
}

func sanitize(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	return name
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic("missing " + k)
	}
	return v
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func tryShrinkGIF(inPath string) {
	// если есть gifsicle — попробуем слегка ужать (lossy=60). Ошибки игнорим.
	cmd := exec.Command("gifsicle", "-O3", "--lossy=60", "-o", inPath, inPath)
	_ = cmd.Run()
}

func tryTranscodeGIFtoMP4(gifPath, mp4Path string) error {
	// Неболтливый ffmpeg, совместимый профиль для Telegram
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", gifPath,
		"-movflags", "+faststart",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "26", // ниже — качество выше и размер больше (24..30)
		"-vf", "scale=1024:-2:flags=lanczos,fps=20",
		mp4Path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, string(out))
	}
	return nil
}

// ---------- Help ----------

func helpText() string {
	return `👋 psstelebot: конвертирует GPX → GIF.

Как пользоваться:
1) Пришлите 1..N GPX-файлов документами.
2) Затем:
   • нажмите кнопку снизу «Топографическая карта» или «Спутниковая карта», ИЛИ
   • используйте команды:
/rendertop — tilesPreset=opentopomap
/rendersat — tilesPreset=esri-satellite

Параметры фиксированы:
-size 1024 -fps 20 -duration 15s
-tilesMaxZoom 16 -tilesMinZoom 12
-tilesRetries 3 -tilesRetryBackoff 2s
-tilesTimeout 45s -tilesRPS 0.5 -tilesBurst 1
-tileFit cover -margin 0.02
-lineColors "#e74c3c,#3498db"
-speedOverlay -speedUnits kmh -speedSmooth 1
-lineWidth 3 -timeout 60m

Админам (DEBUG_ADMINS): подробный лог рендера приходит отдельными сообщениями.
/clear — очистить сессию и удалить загруженные GPX.`
}
