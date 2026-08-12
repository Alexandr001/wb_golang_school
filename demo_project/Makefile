.DEFAULT_GOAL := help

BIN_DIR      := bin
SERVICE_BIN  := $(BIN_DIR)/orders-service
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS      := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
GOLANGCI     := $(shell go env GOPATH)/bin/golangci-lint
GOLANGCI_VER := v2.12.2

.PHONY: help
help: ## Показать доступные команды
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Собрать бинарь сервиса в ./bin
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(SERVICE_BIN) ./cmd/orders-service

.PHONY: run
run: ## Запустить сервис локально (нужен .env: cp .env.example .env)
	set -a && . ./.env && set +a && go run ./cmd/orders-service

.PHONY: fmt
fmt: ## Отформатировать код
	go fmt ./...

.PHONY: vet
vet: ## Прогнать go vet
	go vet ./...

.PHONY: lint
lint: $(GOLANGCI) ## Прогнать golangci-lint
	$(GOLANGCI) run

$(GOLANGCI):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VER)

.PHONY: tidy
tidy: ## Привести go.mod/go.sum в порядок
	go mod tidy

.PHONY: clean
clean: ## Удалить артефакты сборки
	rm -rf $(BIN_DIR)

# --- Docker ---------------------------------------------------------------
# VERSION/COMMIT уезжают в бинарь через build args, поэтому в логе старта
# контейнера видно, какая ревизия там запущена.
COMPOSE := VERSION=$(VERSION) COMMIT=$(COMMIT) docker compose

.PHONY: up
up: ## Поднять всё окружение (postgres + kafka + сервис)
	$(COMPOSE) up -d --build

.PHONY: down
down: ## Остановить окружение, данные сохранить
	$(COMPOSE) down

.PHONY: reset
reset: ## Остановить окружение и стереть тома (нужно после правки db/init)
	$(COMPOSE) down -v

.PHONY: logs
logs: ## Смотреть логи сервиса
	$(COMPOSE) logs -f app

.PHONY: ps
ps: ## Показать состояние контейнеров
	$(COMPOSE) ps

.PHONY: psql
psql: ## Открыть psql под пользователем приложения
	# Переменные разворачивает shell внутри контейнера — там они точно те же,
	# что получил postgres, без догадок про содержимое .env на хосте.
	$(COMPOSE) exec postgres sh -c 'psql -U "$$APP_DB_USER" -d "$$POSTGRES_DB"'
