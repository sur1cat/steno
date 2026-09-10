package core

import (
	"fmt"

	"github.com/sur1cat/steno/internal/i18n"
)

// Учёт расхода на Claude. Токены берутся из ответа API — они точные. Цена
// считается по таблице из конфига: она меняется чаще, чем выходят релизы, и
// зашивать её в бинарник значит однажды показывать неправду.
//
// Главное, что видно из этих чисел: при adaptive thinking рассуждение модели
// тарифицируется как выход, и на длинном созвоне оно, а не сам follow-up,
// определяет счёт. Поэтому `effort` — первый рычаг, если дорого.

type Price struct {
	Input      float64 `json:"input"`      // $ за миллион входных токенов
	Output     float64 `json:"output"`     // $ за миллион выходных
	CacheRead  float64 `json:"cache_read"` // $ за миллион прочитанных из кеша
	CacheWrite float64 `json:"cache_write"`
}

// Цены на 2026-06-24. Обновляются правкой claude.prices в конфиге.
func defaultPrices() map[string]Price {
	opus := Price{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}
	return map[string]Price{
		"claude-opus-5":     opus,
		"claude-opus-4-8":   opus,
		"claude-opus-4-7":   opus,
		"claude-opus-4-6":   opus,
		"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
		"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
		"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5},
		"claude-sonnet-4-6": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-haiku-4-5":  {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
	}
}

// Spend — что стоил один follow-up.
type Spend struct {
	Model      string  `json:"model"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	USD        float64 `json:"usd"`
	PriceKnown bool    `json:"price_known"`
}

func ComputeSpend(cfg *Config, model string, in, out, cacheRead, cacheWrite int64) Spend {
	s := Spend{Model: model, Input: in, Output: out,
		CacheRead: cacheRead, CacheWrite: cacheWrite}
	prices := cfg.Claude.Prices
	if prices == nil {
		prices = defaultPrices()
	}
	p, ok := prices[model]
	if !ok {
		return s // цену не знаем — токены всё равно сохраним
	}
	s.PriceKnown = true
	const m = 1_000_000
	s.USD = float64(in)/m*p.Input +
		float64(out)/m*p.Output +
		float64(cacheRead)/m*p.CacheRead +
		float64(cacheWrite)/m*p.CacheWrite
	return s
}

func (s Spend) String() string {
	// Токенов может не быть вовсе: не всякий источник их отдаёт, а «вход 0,
	// выход 0 — $0.069» читается как ошибка учёта, хотя цена тут верная.
	if s.Input == 0 && s.Output == 0 && s.CacheRead == 0 && s.CacheWrite == 0 {
		if !s.PriceKnown {
			return i18n.Tr("неизвестен")
		}
		return fmt.Sprintf("$%.3f", s.USD)
	}
	base := fmt.Sprintf(i18n.Tr("вход %d, выход %d"), s.Input, s.Output)
	if s.CacheRead > 0 || s.CacheWrite > 0 {
		base += fmt.Sprintf(i18n.Tr(", кеш %d/%d"), s.CacheRead, s.CacheWrite)
	}
	if !s.PriceKnown {
		return base + fmt.Sprintf(i18n.Tr(" (цена для %s неизвестна — добавь в claude.prices)"), s.Model)
	}
	return base + fmt.Sprintf(" — $%.3f", s.USD)
}
