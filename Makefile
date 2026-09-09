BINARY := steno

.PHONY: build panel panel-dev test test-js bot-image clean

# Бинарник со вшитой панелью. Фронт собирается первым: бандл попадает внутрь
# через go:embed, и без пересборки в бинарник уедет прошлый — или пустота.
build: panel
	go build -trimpath -o $(BINARY) .

# Сборка панели. Нужен node — только здесь и только на сборке: в проде steno
# остаётся одним файлом, второго процесса и node_modules там нет.
#
# Без node, но с уже собранным бандлом, сборка не падает: так `go install` и
# `make build` продолжают работать у того, кто получил репозиторий с готовым
# web/dist. А вот молча собрать бинарник с пустой панелью — хуже, чем
# остановиться: «панель не открывается» разбирают потом часами.
panel:
	@if command -v npm >/dev/null 2>&1; then \
		cd web/app && npm ci --no-audit --no-fund && npx vite build; \
	elif [ -f web/dist/index.html ]; then \
		echo "node не найден — беру уже собранный web/dist"; \
	else \
		echo "нужен node: панель не собрана, и собрать её нечем" >&2; \
		echo "поставь node (brew install node) или возьми готовый релиз" >&2; \
		exit 1; \
	fi

# Панель в разработке: живая пересборка на 5273, запросы к API уходят в
# запущенный рядом `steno serve`.
panel-dev:
	cd web/app && npx vite dev

test:
	go test ./...

# Разбор DOM площадок на синтетическом дереве. Нужен только node; настоящий
# браузер не поднимается. Эти же файлы гоняет `go test` (TestPageScriptsOnFixtures),
# отдельная цель нужна, чтобы видеть их вывод целиком.
test-js:
	node meet_test.mjs
	node jitsi_test.mjs

# Образ бота: Chromium + PulseAudio + ffmpeg + этот же бинарник.
bot-image:
	docker build -f docker/Dockerfile -t steno-bot:latest .

clean:
	rm -f $(BINARY)
	rm -rf web/dist/assets web/dist/index.html
