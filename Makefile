.PHONY: build test lint dev build-web dev-server android-test android-apk

# CGO is always disabled for clean cross-compilation.
export CGO_ENABLED=0

build: build-web
	@mkdir -p dist
	cd server && go build -o ../dist/bowtie ./cmd/bowtie

build-web:
	cd web && npm ci && npm run build

test:
	cd server && go test ./...
	cd web && npm ci && npm test

lint:
	cd server && golangci-lint run ./...
	cd web && npm run lint

# Vite dev server (proxies /api → :8400). Run the Go server separately:
#   make dev-server
dev:
	cd web && npm run dev

dev-server:
	cd server && go run ./cmd/bowtie --data-dir ../data

# Android (JDK 17 + Android SDK required; uses android/gradlew)
android-test:
	cd android && ./gradlew :core:test :app:testDebugUnitTest

android-apk:
	cd android && ./gradlew :app:assembleDebug

