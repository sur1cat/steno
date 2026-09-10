BINARY := steno

.PHONY: build panel panel-dev test test-js bot-image clean

# Бинарник со вшитой панелью. Фронт собирается первым: бандл попадает внутрь
# через go:embed (internal/panel), и без пересборки в бинарник уедет прошлый.
build: panel
	go build -trimpath -o $(BINARY) ./cmd/steno

# Сборка панели. Нужен node — только здесь и только на сборке: в проде steno
# остаётся одним файлом, второго процесса и node_modules там нет.
#
# Без node, но с уже собранным бандлом, сборка не падает: так `go install` и
# `make build` продолжают работать у того, кто получил репозиторий с готовым
# бандлом internal/panel/web/dist. А вот молча собрать бинарник с пустой
# панелью — хуже, чем остановиться: «панель не открывается» разбирают часами.
panel:
	@if command -v npm >/dev/null 2>&1; then \
		cd internal/panel/web/app && npm ci --no-audit --no-fund && npx vite build; \
	elif [ -f internal/panel/web/dist/index.html ]; then \
		echo "node не найден — беру уже собранный internal/panel/web/dist"; \
	else \
		echo "нужен node: панель не собрана, и собрать её нечем" >&2; \
		echo "поставь node (brew install node) или возьми готовый релиз" >&2; \
		exit 1; \
	fi

# Панель в разработке: живая пересборка на 5273, запросы к API уходят в
# запущенный рядом `steno serve`.
panel-dev:
	cd internal/panel/web/app && npx vite dev

test:
	go test ./...

# Разбор DOM площадок на синтетическом дереве. Нужен только node; настоящий
# браузер не поднимается. Эти же файлы гоняет `go test` (TestPageScriptsOnFixtures),
# отдельная цель нужна, чтобы видеть их вывод целиком.
#
# Из каталога пакета: скрипты читают selectors.json рядом с собой.
test-js:
	cd internal/bot && node meet_test.mjs
	cd internal/bot && node jitsi_test.mjs

# Образ бота: Chromium + PulseAudio + ffmpeg + этот же бинарник.
#
# Запасной путь. Готовый образ каждой версии лежит в ghcr.io, и steno тянет его
# сам перед первым созвоном; собирать руками нужно только тому, кто правит
# самого бота или сидит там, откуда ghcr.io недоступен.
bot-image:
	docker build -f docker/Dockerfile -t steno-bot:latest .

clean:
	rm -f $(BINARY)
	rm -rf internal/panel/web/dist/assets internal/panel/web/dist/index.html
