set -x

CGO_ENABLED=0 GOOS=linux GOARCH=arm go build -ldflags="-s -w" -o bridge-armhf .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bridge-arm64 .
