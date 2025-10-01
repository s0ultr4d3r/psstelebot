package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Автоматическая подгрузка .env перед main().
// Порядок поиска:
// 1) PSS_DOTENV (если указан путь явно)
// 2) Текущая рабочая директория (откуда запустили бинарь)
// 3) Директория бинаря
// 4) Поднимаемся вверх от CWD до корня файловой системы и ищем первый ".env"
func init() {
	// Если токен уже есть в окружении — ничего не делаем.
	if os.Getenv("BOT_TOKEN") != "" {
		return
	}

	// 1) Явно заданный путь
	if p := strings.TrimSpace(os.Getenv("PSS_DOTENV")); p != "" {
		if tryLoadEnv(p) {
			return
		}
	}

	// 2) CWD
	if wd, err := os.Getwd(); err == nil {
		if tryLoadEnv(filepath.Join(wd, ".env")) {
			return
		}
	}

	// 3) Директория бинаря
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if tryLoadEnv(filepath.Join(exeDir, ".env")) {
			return
		}
	}

	// 4) Поиск вверх от CWD
	if wd, err := os.Getwd(); err == nil {
		if p := findDotenvUp(wd); p != "" && tryLoadEnv(p) {
			return
		}
	}

	// Если сюда дошли — не нашли .env. Логнем, но не падаем:
	if os.Getenv("BOT_TOKEN") == "" {
		log.Printf("BOT_TOKEN is empty (no .env loaded). " +
			"Укажи переменную окружения или положи .env в корень проекта и запусти из него.\n" +
			"Также можно задать путь переменной PSS_DOTENV=/path/to/.env")
	}
}

func tryLoadEnv(path string) bool {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		if err := godotenv.Load(path); err == nil {
			log.Printf("loaded .env from %s", path)
			return true
		}
	}
	return false
}

// findDotenvUp поднимается по дереву директорий вверх от startDir и
// возвращает первый найденный путь к ".env". Если не найден — "".
func findDotenvUp(startDir string) string {
	dir := startDir
	for {
		p := filepath.Join(dir, ".env")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// дошли до корня
			return ""
		}
		dir = parent
	}
}
