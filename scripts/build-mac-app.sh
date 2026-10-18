#!/bin/sh
# Builds bin/Kopi.app, a menu-bar app bundle, and optionally installs it.
#   scripts/build-mac-app.sh [version] [--install]
# The bundle matters: Info.plist declares the Bonjour service and the Local Network
# usage text, which macOS needs before it will let Kopi talk to other devices.
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
INSTALL="${2:-}"
APP=bin/Kopi.app

command -v go >/dev/null 2>&1 || PATH="$PATH:$HOME/sdk/go/bin:/usr/local/go/bin"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
	-o "$APP/Contents/MacOS/kopi" ./cmd/kopi

cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Kopi</string>
	<key>CFBundleDisplayName</key><string>Kopi</string>
	<key>CFBundleIdentifier</key><string>io.github.umidscodes.kopi</string>
	<key>CFBundleExecutable</key><string>kopi</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
	<key>CFBundleVersion</key><string>${VERSION#v}</string>
	<key>LSMinimumSystemVersion</key><string>13.0</string>
	<key>LSUIElement</key><true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>Kopi finds your other devices on this network to keep your clipboards in sync.</string>
	<key>NSBonjourServices</key>
	<array><string>_kopi._tcp</string></array>
</dict>
</plist>
EOF

# Ad-hoc signature: required on Apple Silicon, and gives macOS a stable identity for
# the Local Network permission. (Not notarized; see README for the Gatekeeper bypass.)
codesign --force --deep --sign - "$APP"
echo "Built $APP ($VERSION)"

if [ "$INSTALL" = "--install" ]; then
	mkdir -p "$HOME/Applications"
	pkill -x kopi 2>/dev/null || true
	rm -rf "$HOME/Applications/Kopi.app"
	cp -R "$APP" "$HOME/Applications/"
	echo "Installed to ~/Applications/Kopi.app"
fi
