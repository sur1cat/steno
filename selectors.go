package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

//go:embed meet.js
var meetJS string

//go:embed jitsi.js
var jitsiJS string

//go:embed selectors.json
var defaultSelectorsJSON []byte

// Selectors — всё, что зависит от вёрстки и локали площадок, живёт здесь.
// Когда Google или Jitsi переедут, чинится правкой selectors.json без
// пересборки.
//
// Поля Meet остались на верхнем уровне, а не переехали в свою секцию: у людей
// уже лежат свои selectors.json, и переименование сломало бы их молча — бот
// просто перестал бы находить кнопку входа.
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

	// Jitsi — своя секция: другая вёрстка, другие подписи и, в отличие от
	// Meet, список адресов. Одного канонического адреса у Jitsi нет, его
	// ставят себе — поэтому свой сервер прописывается сюда.
	Jitsi JitsiSelectors `json:"jitsi"`
}

// JitsiSelectors — подписи кнопок Jitsi. Атрибуты (data-testid, id, классы) в
// selectors.json не вынесены намеренно: они не зависят от языка и защищены
// собственными e2e-тестами Jitsi, а вот подписи меняются вместе с локалью.
type JitsiSelectors struct {
	// Адреса своих серверов Jitsi: "jitsi.example.com". Публичные meet.jit.si
	// и 8x8.vc известны и без списка.
	Hosts []string `json:"hosts"`
	// Подпись кнопки, когда микрофон или камера ВКЛЮЧЕНЫ («выключить
	// микрофон»): по ней бот понимает, что надо нажать.
	LiveMicLabels []string `json:"liveMicLabels"`
	LiveCamLabels []string `json:"liveCamLabels"`
	// Подпись, когда они уже выключены («включить микрофон»).
	MutedMicLabels []string `json:"mutedMicLabels"`
	MutedCamLabels []string `json:"mutedCamLabels"`
	// Меню «Ещё»: кнопка субтитров обычно спрятана в нём.
	MoreActionsLabels   []string `json:"moreActionsLabels"`
	CaptionButtonLabels []string `json:"captionButtonLabels"`
	// Текст, по которому видно, что нас вывели из звонка.
	LeftMeetingTexts []string `json:"leftMeetingTexts"`
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
