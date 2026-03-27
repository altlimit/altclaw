#!/bin/bash
set -e
if [ -z "$VERSION" ]; then
    VERSION="dev"
fi
echo "Building version $VERSION..."
rm -rf ./build

# Frontend build first
echo "Building full stack..."
cd vue
npm install
npm run build
cd ..

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/win/altclaw.exe ./cmd/altclaw/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/linux/altclaw ./cmd/altclaw/
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/linux-arm64/altclaw ./cmd/altclaw/
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/darwin/altclaw ./cmd/altclaw/
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/darwin-arm64/altclaw ./cmd/altclaw/

# GUI builds (optional — requires CGO_ENABLED=1 and system webview libs)
# Linux: apt install libgtk-3-dev libwebkit2gtk-4.1-dev
# macOS: Xcode command line tools  |  Windows: no extra deps (uses WebView2)
if [ "$BUILD_GUI" = "1" ]; then
    echo "Building GUI variants..."
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -tags gui -ldflags "-s -w -H windowsgui -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/win/altclaw-gui.exe ./cmd/altclaw/
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags gui -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/linux/altclaw-gui ./cmd/altclaw/
    CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags gui -ldflags "-s -w -X altclaw.ai/internal/buildinfo.Version=$VERSION" -o build/darwin-arm64/altclaw-gui ./cmd/altclaw/
fi

cd build/win
zip -rq ../win.zip . -x ".*"
cd ../linux
tar -czf ../linux.tgz altclaw
cd ../linux-arm64
tar -czf ../linux-arm64.tgz altclaw
cd ../darwin
tar -czf ../darwin.tgz altclaw
cd ../darwin-arm64
tar -czf ../darwin-arm64.tgz altclaw
cd ..
rm -rf darwin
rm -rf darwin-arm64
rm -rf linux
rm -rf linux-arm64
rm -rf win
echo "Done"
