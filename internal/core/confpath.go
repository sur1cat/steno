package core

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sur1cat/steno/internal/i18n"
)

// Где лежит настройка, если её не назвали флагом.
//
// steno искал steno.json только в текущем каталоге. Для программы, поставленной
// через brew, это неверно: человек настраивает её один раз, а команды набирает
// откуда придётся. Запущенный из домашнего каталога `steno serve` молча брал
// умолчания — с выключенной панелью и базой в ./data, — и выглядело это как
// «панель не открывается» и «проект не сохранился».
//
// Поэтому setup оставляет указатель на выбранный каталог, а команды без -c
// смотрят сначала рядом с собой, потом по указателю.

func stenoPointerPath() string {
	// Через os.UserHomeDir, то есть через $HOME: тест обязан подменять его на
	// свой временный каталог. Раньше мастер установки, запущенный из `go test`,
	// переписывал указатель настоящего человека путём в /var/folders/.../
	// TestSetup…/001 — каталог тут же исчезал, а `steno start` после этого молча
	// поднимался на умолчаниях, без панели и источников. Один прогон тестов
	// ломал рабочую установку.
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "steno", "path")
	}
	return ""
}

// RememberConfigPath запоминает, где лежит настройка. Ошибку не возвращаем:
// не записался указатель — команды просто продолжат искать в текущем каталоге,
// как раньше, и это не повод ронять установку.
func RememberConfigPath(configPath string) {
	p := stenoPointerPath()
	if p == "" {
		return
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, []byte(abs+"\n"), 0o644)
	}
}

// rememberedConfigPath — что записал setup, если этот файл ещё на месте.
func rememberedConfigPath() string {
	p := stenoPointerPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return ""
	}
	if _, err := os.Stat(v); err != nil {
		return "" // каталог убрали — молча возвращаемся к поиску рядом
	}
	return v
}

// ResolveConfigPath — единственное место, где решается, какой файл настройки
// брать. Раньше это решение принимал open(), а doctor судил по флагу и потому
// печатал «конфига нет» ровно тогда, когда настройка нашлась по указателю.
func ResolveConfigPath(configPath string) string {
	if _, err := os.Stat(configPath); err == nil {
		return configPath
	}
	if configPath != DefaultConfigPath {
		return configPath // назвали явно — не подменяем молча
	}
	if p := rememberedConfigPath(); p != "" {
		return p
	}
	return configPath
}

// MeetingArg — id созвона из аргументов, а если его не назвали, то последний.
//
// Идти за id в `steno list`, копировать строку и возвращаться — работа, которую
// человек делает каждый раз, хотя в девяти случаях из десяти нужен именно
// последний созвон. Молча брать последний нельзя: человек должен видеть, с чем
// работает, иначе `steno process` без аргумента однажды пережуёт не то.
func MeetingArg(st *Store, rest []string) (string, error) {
	if len(rest) > 0 && strings.TrimSpace(rest[0]) != "" {
		return rest[0], nil
	}
	id, title, err := st.LatestMeeting()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New(i18n.Tr("созвонов пока нет"))
	}
	log.Printf(i18n.Tr("последний созвон: %s%s"), id, OrEmpty(title, " — "+title))
	return id, nil
}

func OrEmpty(v, s string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return s
}

// AdapterPath чинит путь к адаптеру, если он перестал существовать.
//
// В конфиг записывается абсолютный путь, а у поставленного через brew он ведёт
// внутрь каталога с номером версии: /opt/homebrew/Cellar/steno/0.1.2/share/…
// После `brew upgrade` этот каталог удаляется вместе со старой версией, и
// расшифровка отваливается с «No such file or directory» — на первом же созвоне
// после обновления. Ровно это и случилось.
//
// Поэтому путь из конфига — не приговор, а подсказка: если файла там нет, ищем
// адаптер с тем же именем там, где он лежит сейчас. Чужие команды не трогаем:
// подменять человеку его собственный скрипт мы не вправе.
func AdapterPath(p string) string {
	if p == "" {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	name := filepath.Base(p)
	switch name {
	case "whisper-cpp.sh", "groq.sh", "faster-whisper.py", "assemblyai.sh", "discord.sh":
	default:
		return p
	}
	if found := FindAdapter(name); found != p {
		if _, err := os.Stat(found); err == nil {
			log.Printf(i18n.Tr("адаптер переехал: беру %s"), found)
			return found
		}
	}
	return p
}

const DefaultConfigPath = "steno.json"

// FindAdapter ищет адаптер расшифровки и возвращает путь, который сработает из
// любого каталога.
//
// Раньше в конфиг писалось «./adapters/whisper-cpp.sh» — относительный путь,
// живущий только внутри клона репозитория. У поставившего через brew файла по
// этому пути нет вовсе, и распознавание молча оказывалось неработающим: doctor
// говорил «не запускается», а откуда взять — нет.
//
// Порядок понятный: рядом с текущим каталогом (разработка), рядом с самим
// бинарником, и в share пакета — туда их кладёт формула Homebrew.
func FindAdapter(name string) string {
	var roots []string
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(wd, "adapters"))
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			roots = append(roots,
				filepath.Join(dir, "adapters"),
				// Cellar/steno/<версия>/bin/steno → .../share/steno/adapters
				filepath.Join(dir, "..", "share", "steno", "adapters"),
			)
		}
	}
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		roots = append(roots, filepath.Join(p, "share", "steno", "adapters"))
	}
	roots = append(roots, "/opt/homebrew/share/steno/adapters", "/usr/local/share/steno/adapters")

	for _, r := range roots {
		p := filepath.Join(r, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	// Не нашли — оставляем прежний вид, чтобы doctor сказал об этом словами, а
	// конфиг остался читаемым и правился руками.
	return filepath.Join("adapters", name)
}
