package http

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"time"
)

// requestIDHeader — заголовок, по которому запрос можно связать с логами.
const requestIDHeader = "X-Request-Id"

// maxRequestIDLen ограничивает длину чужого request-id: он уезжает в логи.
const maxRequestIDLen = 64

// statusClientClosed — код для лога, когда клиент отвалился, не дождавшись ответа.
// В сеть не уходит и в IANA его нет, но соглашение nginx понимают все.
const statusClientClosed = 499

type ctxKey int

const requestIDKey ctxKey = iota

// middleware — обёртка вокруг обработчика.
type middleware func(http.Handler) http.Handler

// chain собирает цепочку: первый переданный middleware оказывается внешним.
func chain(handler http.Handler, middlewares ...middleware) http.Handler {
	for _, wrap := range slices.Backward(middlewares) {
		handler = wrap(handler)
	}

	return handler
}

// assignRequestID кладёт идентификатор запроса в context и в ответ. Чужой
// X-Request-Id принимаем только после проверки: непроверенный ввод в логе
// ломает разбор переводами строки и раздувает записи длиной.
func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		if !validRequestID(requestID) {
			// rand.Text даёт 26 символов base32 и не возвращает ошибку.
			requestID = rand.Text()
		}

		w.Header().Set(requestIDHeader, requestID)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLen {
		return false
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}

	return true
}

// requestIDFrom достаёт идентификатор запроса из context.
func requestIDFrom(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)

	return requestID
}

// logRequests пишет access-лог. Стоит снаружи таймаута и recover'а: иначе
// запросы, оборвавшиеся по таймауту или панике, в лог бы не попали — то есть
// ровно те, ради которых лог и читают.
//
// quietPaths — маршруты, которым хватает debug: probe'и ходят раз в несколько
// секунд и на info забили бы лог целиком. Список приходит от роутера, а не
// зашит здесь: маршрут объявляется в одном месте, и его свойства тоже.
func logRequests(log *slog.Logger, quietPaths ...string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := newResponseRecorder(w)

			next.ServeHTTP(recorder, r)

			status := recorder.Status()

			// Клиент ушёл, не дождавшись ответа. Реальный код писать нельзя:
			// http.TimeoutHandler всё равно вызовет WriteHeader(503), и лог
			// сообщал бы об ошибке сервера там, где сервер ни при чём.
			// Свой таймаут обработчика сюда не попадает — он отменяет
			// производный context, а этот принадлежит соединению.
			if errors.Is(r.Context().Err(), context.Canceled) {
				status = statusClientClosed
			}

			log.Log(r.Context(), levelFor(status, slices.Contains(quietPaths, r.URL.Path)), "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", recorder.bytes),
				slog.Duration("took", time.Since(start)),
				slog.String("request_id", requestIDFrom(r.Context())),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// levelFor выбирает уровень записи. Ошибки видно всегда, даже на «тихом»
// маршруте: замалчивать стоит рутину, а не поломку.
func levelFor(status int, quiet bool) slog.Level {
	switch {
	// Ушедший клиент — обычное дело, хотя код и лежит в диапазоне 4xx.
	case status == statusClientClosed:
		return slog.LevelInfo
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case quiet:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// recoverPanic превращает панику в 500 и оставляет в логе стек. net/http ловит
// её и сам, но молча рвёт соединение: клиент остаётся без ответа.
func recoverPanic(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					reportPanic(w, log, r, recovered)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// reportPanic пишет панику в лог и, если ответ ещё не начат, отдаёт 500.
func reportPanic(w http.ResponseWriter, log *slog.Logger, r *http.Request, recovered any) {
	// ErrAbortHandler — договорённость stdlib «оборви соединение молча».
	if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(recovered)
	}

	ctx := r.Context()

	log.ErrorContext(ctx, "panic in handler",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("request_id", requestIDFrom(ctx)),
		slog.Any("panic", recovered),
		slog.String("stack", string(debug.Stack())),
	)

	// Если ответ уже начали, менять статус поздно: заголовки ушли.
	if written, ok := w.(interface{ Written() bool }); ok && written.Written() {
		return
	}

	writeError(w, log, http.StatusInternalServerError, "internal error")
}

// limitDuration ограничивает время работы обработчика: TimeoutHandler отменяет
// context и отдаёт 503, так что зависший запрос не держит соединение до WriteTimeout.
func limitDuration(timeout time.Duration) middleware {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}

		return http.TimeoutHandler(next, timeout, timeoutBody)
	}
}

// responseRecorder запоминает статус и объём ответа для лога.
type responseRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w}
}

func (r *responseRecorder) WriteHeader(status int) {
	// Запоминаем первый статус: лог должен показать реально отданный код.
	if r.status == 0 {
		r.status = status
	}

	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	written, err := r.ResponseWriter.Write(body)
	r.bytes += written

	return written, err
}

// Written сообщает, ушли ли заголовки: recoverPanic не должен дописывать 500
// поверх начатого ответа.
func (r *responseRecorder) Written() bool {
	return r.status != 0
}

// Status отдаёт код ответа. Ноль означает, что обработчик не написал ничего,
// и net/http отправит 200 с пустым телом.
func (r *responseRecorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}

// Unwrap открывает http.ResponseController доступ к исходному ResponseWriter:
// без этого обёртка ломает Flush и SetWriteDeadline.
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
