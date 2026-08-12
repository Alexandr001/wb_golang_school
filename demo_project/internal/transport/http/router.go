package http

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

const (
	pathHealthz = "/healthz"
	pathReadyz  = "/readyz"
)

// staticFiles — веб-страница внутри бинарника: тома с ассетами не нужно.
// Каталог лежит рядом с пакетом, выше своей директории embed смотреть не умеет.
//
//go:embed static
var staticFiles embed.FS

// staticRoot — содержимое static/ без префикса пути. Ошибку не тащим наружу:
// путь константный и проверен на этапе сборки, ветка в main не сработала бы никогда.
var staticRoot = mustSub(staticFiles, "static")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("embed %s: %v", dir, err))
	}

	return sub
}

// NewRouter собирает маршруты и оборачивает их в middleware.
//
// Порядок снаружи внутрь: request-id → access-лог → recover → таймаут. Лог стоит
// снаружи recover'а и таймаута, иначе упавшие и подвисшие запросы в него не
// попадут; request-id — ещё выше, чтобы попасть в саму запись лога.
func NewRouter(h *Handler, handlerTimeout time.Duration, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Роутер stdlib с 1.22 умеет и метод, и путевые параметры.
	mux.HandleFunc("GET /order/{order_uid}", h.GetOrder)
	mux.HandleFunc("GET "+pathHealthz, h.Healthz)
	mux.HandleFunc("GET "+pathReadyz, h.Readyz)

	// Всё непойманное выше уходит в статику: "/" отдаёт index.html.
	mux.Handle("GET /", http.FileServerFS(staticRoot))

	return chain(mux,
		assignRequestID,
		// Тихие маршруты объявляет тот, кто их регистрирует, иначе про новый
		// probe здесь вспомнят, а про лог забудут.
		logRequests(log, pathHealthz, pathReadyz),
		recoverPanic(log),
		limitDuration(handlerTimeout),
	)
}
