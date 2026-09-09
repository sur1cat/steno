BINARY := steno

.PHONY: build test test-js bot-image clean

build:
	go build -trimpath -o $(BINARY) .

test:
	go test ./...

# Разбор DOM субтитров Meet на синтетическом дереве. Нужен только node.
test-js:
	node meet_test.mjs

# Образ бота: Chromium + PulseAudio + ffmpeg + этот же бинарник.
bot-image:
	docker build -f docker/Dockerfile -t steno-bot:latest .

clean:
	rm -f $(BINARY)
