#!/bin/sh
# Собирает StenoBar.app — steno в строке меню macOS.
#
# Отдельно от Makefile и от brew: это приложение для конкретного мака, а не
# часть сервиса. `make` и `brew install steno` его не трогают и трогать не
# должны — на сервере ему делать нечего.
#
#   ./build.sh && open build/StenoBar.app
#
# Нужны Xcode command line tools и macOS 14+.
set -eu
cd "$(dirname "$0")"

APP="build/StenoBar.app"
BIN="$APP/Contents/MacOS"
rm -rf build
mkdir -p "$BIN"

echo "собираю…"
swiftc -O -parse-as-library -target arm64-apple-macos14.0 \
  -o "$BIN/steno-bar" Sources/*.swift

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>StenoBar</string>
  <key>CFBundleDisplayName</key><string>steno</string>
  <key>CFBundleIdentifier</key><string>dev.sur1cat.steno.bar</string>
  <key>CFBundleExecutable</key><string>steno-bar</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <!-- Без иконки в доке: у приложения строки меню окна нет вовсе. -->
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
  <!-- Панель по умолчанию слушает http на петле. ATS петлю и так пропускает,
       но панель бывает и на 192.168.x.x — это разрешение про такой случай, а
       не про произвольный http куда угодно. -->
  <key>NSAppTransportSecurity</key>
  <dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
PLIST

# Подпись ad-hoc: для локального запуска её достаточно, а без неё macOS
# ругается на неподписанный бинарник при каждом запуске.
codesign --force --sign - "$APP" 2>/dev/null || true
echo "собрано: $APP"
