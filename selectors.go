package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

//go:embed meet.js
var meetJS string

//go:embed selectors.json
var defaultSelectorsJSON []byte

// Selectors — всё, что зависит от вёрстки и локали Meet, живёт здесь.
// Когда Google переедет, чинится правкой selectors.json без пересборки.
type Selectors struct {
	CaptionRegionLabels   []string `json:"captionRegionLabels"`
	CaptionSettingsLabels []string `json:"captionSettingsLabels"`
	// Как язык называется в интерфейсе Meet. Подпись зависит от языка самого
	// интерфейса: «Русский» у русского, «Russian» у английского, — поэтому
	// для каждого кода держим оба варианта.
	CaptionLanguages     map[string][]string `json:"captionLanguages"`
	CaptionRegionJsnames []string            `json:"captionRegionJsnames"`
	JoinButtonTexts      []string            `json:"joinButtonTexts"`
	NameInputLabels      []string            `json:"nameInputLabels"`
	LeftMeetingTexts     []string            `json:"leftMeetingTexts"`
}

func loadSelectors(path string) (*Selectors, error) {
	raw := defaultSelectorsJSON
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	var s Selectors
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("selectors: %w", err)
	}
	return &s, nil
}

// bootstrapJS — то, что выполняется в странице первым: кладёт селекторы в
// window и определяет window.__steno.
func (s *Selectors) bootstrapJS() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return "window.__stenoSelectors = " + string(b) + ";\n" + meetJS, nil
}
