# ccwin-notify — タスクランナー (just)
# Windows 環境を前提に PowerShell をシェルとして使う。
# `just --list` で利用可能タスクを確認できる。

set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-Command"]

# デフォルトタスク: 利用可能タスクを表示
default:
    @just --list

# ビルド (cmd/ccwin-notify を bin/ccwin-notify.exe へ出力)
build:
    go build -o bin/ccwin-notify.exe ./cmd/ccwin-notify

# テスト (全パッケージ)
test:
    go test ./...

# race detector 付きテスト
test-race:
    go test -race ./...

# カバレッジ (coverage.out + HTML レポート)
cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html

# go fmt
fmt:
    go fmt ./...

# go vet
vet:
    go vet ./...

# fmt -> vet -> test の順に走らせる総合チェック
check: fmt vet test

# 依存整理
mod-tidy:
    go mod tidy

# 生成物の掃除
clean:
    if (Test-Path bin) { Remove-Item -Recurse -Force bin }
    if (Test-Path coverage.out) { Remove-Item -Force coverage.out }
    if (Test-Path coverage.html) { Remove-Item -Force coverage.html }
