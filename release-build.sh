#!/bin/sh

mkdir "Releases"

# 【darwin/amd64】
echo "start build darwin/amd64 ..."
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build  -o ./Releases/m3u8-darwin-amd64 m3u8-downloader.go

# 【linux/amd64】
echo "start build linux/amd64 ..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build  -o ./Releases/m3u8-linux-amd64 m3u8-downloader.go

# 【windows/amd64】
echo "start build windows/amd64 ..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build  -o ./Releases/m3u8-windows-amd64.exe m3u8-downloader.go

echo "Congratulations,all build success!"
