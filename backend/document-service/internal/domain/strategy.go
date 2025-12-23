package domain

import (
	"errors"
	"strings"
)

// ConversionStrategy задаёт стратегию обработки документа.
type ConversionStrategy string

const (
	ConversionStrategyDefault           ConversionStrategy = ""
	ConversionStrategyTablesOnly        ConversionStrategy = "tables_only"
	ConversionStrategyDoclingWithTables ConversionStrategy = "docling_with_tables"
)

// ValidateConversionStrategy проверяет допустимость стратегии конвертации.
func ValidateConversionStrategy(strategy ConversionStrategy) error {
	switch strings.ToLower(strings.TrimSpace(string(strategy))) {
	case "", string(ConversionStrategyTablesOnly), string(ConversionStrategyDoclingWithTables):
		return nil
	default:
		return ErrInvalidConversionStrategy
	}
}

var ErrInvalidConversionStrategy = errors.New("unsupported conversion strategy")

// NormalizeConversionStrategy приводит стратегию к нижнему регистру без пробелов.
func NormalizeConversionStrategy(strategy string) ConversionStrategy {
	return ConversionStrategy(strings.ToLower(strings.TrimSpace(strategy)))
}
