// Package logger настраивает структурное логирование на базе log/slog.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New собирает *slog.Logger по строковым level и format ("json"|"text").
// Неизвестные значения отбрасываются к info/json: их отсекает config.Load.
func New(w io.Writer, level, format string) *slog.Logger {
	parsed, err := ParseLevel(level)
	if err != nil {
		parsed = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: parsed,
		// На info источник — лишний шум и лишний runtime.Caller на каждую запись.
		AddSource: parsed == slog.LevelDebug,
	}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler)
}

// ParseLevel разбирает название уровня силами самого slog: свой switch означал бы
// второй список названий рядом с тем, что в config.validate.
func ParseLevel(level string) (slog.Level, error) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return 0, fmt.Errorf("parse log level %q: %w", level, err)
	}

	return parsed, nil
}
