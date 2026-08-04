.PHONY: build test lint dev build-web

# CGO is always disabled for clean cross-compilation.
export CGO_ENABLED=0

build: build-web
	@mkdir -p dist
	cd server && go build -o ../dist/bowtie ./cmd/bowtie

build-web:
	@if [ -d web ] && [ -f web/package.json ]; then \
		cd web && npm ci && npm run build; \
	else \
		echo "web not yet scaffolded"; \
	fi

test:
	cd server && go test ./...
	@if [ -d web ] && [ -f web/package.json ]; then \
		cd web && npm test; \
	else \
		echo "web not yet scaffolded"; \
	fi

lint:
	cd server && golangci-lint run ./...
	@if [ -d web ] && [ -f web/package.json ]; then \
		cd web && npm run lint; \
	else \
		echo "web not yet scaffolded"; \
	fi

dev:
	@if [ -d web ] && [ -f web/package.json ]; then \
		cd web && npm run dev; \
	else \
		echo "web not yet scaffolded — starting server only"; \
		cd server && go run ./cmd/bowtie --data-dir ../data; \
	fi
