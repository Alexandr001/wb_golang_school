package domain

import "errors"

var (
	// ErrNotFound — заказа нет ни в кэше, ни в БД; транспорт отдаёт 404.
	ErrNotFound = errors.New("order not found")

	// ErrInvalidMessage — сообщение не разобралось или не прошло валидацию.
	ErrInvalidMessage = errors.New("invalid message")
)

// permanentError помечает ошибку как неисправимую повтором. Тип неэкспортируемый:
// снаружи работают через Permanent/IsPermanent.
type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }

func (e permanentError) Unwrap() error { return e.err }

// Permanent помечает ошибку как permanent. Обёртка прозрачна для errors.Is/As.
func Permanent(err error) error {
	if err == nil {
		return nil
	}

	return permanentError{err: err}
}

// IsPermanent отвечает на единственный вопрос consumer'а: ретраить или сдаться.
// Всё неопознанное — transient: неизвестную ошибку безопаснее повторить, чем потерять.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}

	var permanent permanentError
	if errors.As(err, &permanent) {
		return true
	}

	return errors.Is(err, ErrInvalidMessage)
}
