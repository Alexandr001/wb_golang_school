# syntax=docker/dockerfile:1

# --- Сборка ---------------------------------------------------------------
FROM golang:1.26.5-alpine AS build

WORKDIR /src

# Зависимости отдельным слоем: пока go.mod/go.sum не менялись, слой берётся
# из кэша и повторная сборка не ходит в сеть.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

COPY . .

# Значения прокидываются из docker-compose (см. build.args) и попадают
# в лог старта сервиса — по ним видно, какая ревизия сейчас запущена.
ARG VERSION=dev
ARG COMMIT=none

# CGO_ENABLED=0 — статический бинарь: без него distroless/static не запустит его,
# там нет ни glibc, ни musl. -trimpath убирает пути сборочной машины.
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
	-o /out/orders-service ./cmd/orders-service

# --- Рантайм --------------------------------------------------------------
# distroless/static: ни shell'а, ни пакетного менеджера — нечего эксплуатировать
# и нечего обновлять. Тег nonroot запускает процесс под uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot

# Миграции и веб-страница вкомпилированы в бинарь через embed.FS,
# поэтому в образе больше ничего не нужно.
COPY --from=build /out/orders-service /orders-service

USER nonroot:nonroot

EXPOSE 8081

ENTRYPOINT ["/orders-service"]
