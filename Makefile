.PHONY: build backend frontend frontend-build tidy clean dev help

# Production: build frontend, then Go binary that embeds web/dist
build: frontend-build
	go build -o bin/periscope ./cmd/periscope

# Run the Go backend (port 8080)
backend:
	go run ./cmd/periscope

# Run the Vite dev server (port 5173, proxies /api -> backend)
frontend:
	cd web && npm run dev

# Build the frontend bundle (consumed by `build` for embedding)
frontend-build:
	cd web && npm run build

tidy:
	go mod tidy
	cd web && npm install

clean:
	rm -rf bin/ web/dist/ web/node_modules/

help:
	@echo "Run 'make backend' and 'make frontend' in separate terminals during dev."
	@echo "Run 'make build' to produce the single embedded production binary."

dev: help
