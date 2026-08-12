package domain

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// Validator проверяет доменные модели по validate-тегам.
// Создавать один раз на приложение: разбор структур кэшируется, Struct потокобезопасен.
type Validator struct {
	validate *validator.Validate
}

// NewValidator собирает валидатор с настройками проекта.
func NewValidator() *Validator {
	validate := validator.New(validator.WithRequiredStructEnabled())

	// Имя поля в ошибке — как в JSON (delivery.email), чтобы лог сопоставлялся
	// с исходным сообщением напрямую.
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}

		return name
	})

	return &Validator{validate: validate}
}

// ValidateOrder проверяет заказ целиком. Нарушение правил — ErrInvalidMessage,
// то есть permanent.
func (v *Validator) ValidateOrder(order *Order) error {
	err := v.validate.Struct(order)
	if err == nil {
		return nil
	}

	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		// Ошибка использования (передали не структуру) — это баг, а не битые данные.
		return fmt.Errorf("validate order: %w", err)
	}

	return fmt.Errorf("%w: %s", ErrInvalidMessage, formatValidationErrors(validationErrs))
}

// formatValidationErrors собирает список нарушений вида
// `order_uid (required), items[0].price (gte=0)`.
func formatValidationErrors(validationErrs validator.ValidationErrors) string {
	parts := make([]string, 0, len(validationErrs))

	for _, fieldErr := range validationErrs {
		// Namespace начинается с имени корневой структуры (`Order.`) — оно шумит.
		field := fieldErr.Namespace()
		if _, rest, found := strings.Cut(field, "."); found {
			field = rest
		}

		rule := fieldErr.Tag()
		if param := fieldErr.Param(); param != "" {
			rule = rule + "=" + param
		}

		parts = append(parts, fmt.Sprintf("%s (%s)", field, rule))
	}

	return strings.Join(parts, ", ")
}
