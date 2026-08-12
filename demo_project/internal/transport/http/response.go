package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const contentTypeJSON = "application/json; charset=utf-8"

// internalErrorBody — ответ на случай, когда сериализация провалилась:
// собирать его тем же кодером наивно, он только что не сработал.
var internalErrorBody = []byte(`{"error":"internal error"}`)

// timeoutBody — тело при срабатывании таймаута обработчика. Content-Type
// http.TimeoutHandler не выставляет, поэтому клиент получит text/plain,
// но тело остаётся валидным JSON: разобрать ошибку важнее заголовка.
const timeoutBody = `{"error":"request timeout"}`

// errorResponse — единый формат ошибки API для 400, 404 и 500.
type errorResponse struct {
	Error string `json:"error"`
}

// healthResponse и readyResponse — тела проверок; типы вместо map,
// потому что состав полей фиксированный.
type healthResponse struct {
	Status string `json:"status"`
}

type readyResponse struct {
	Status    string `json:"status"`
	CacheSize int    `json:"cache_size"`
}

// writeJSON сериализует ответ в буфер и только потом пишет в сеть: при
// кодировании напрямую сбой на середине объекта оставил бы клиенту 200
// и обрезанный JSON — заголовки уже ушли, статус не сменить.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Error("marshal response",
			slog.Int("status", status),
			slog.Any("error", err),
		)

		body, status = internalErrorBody, http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		// Клиент отвалился на середине ответа: сделать нечего, и это не наша поломка.
		log.Debug("write response body", slog.Any("error", err))
	}
}

// writeError отдаёт ошибку тем же JSON'ом, что и успешный ответ.
func writeError(w http.ResponseWriter, log *slog.Logger, status int, message string) {
	writeJSON(w, log, status, errorResponse{Error: message})
}
