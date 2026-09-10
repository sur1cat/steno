package bot

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
	// Путь к языку субтитров в нынешнем Meet: ⋮ («Ещё») → «Настройки» →
	// вкладка «Субтитры». Отдельной кнопки настроек субтитров там больше нет,
	// а подписи на этом пути зависят от языка интерфейса — поэтому здесь.
	MoreOptionsLabels     []string `json:"moreOptionsLabels"`
	SettingsMenuTexts     []string `json:"settingsMenuTexts"`
	CaptionsTabTexts      []string `json:"captionsTabTexts"`
	CaptionLanguageLabels []string `json:"captionLanguageLabels"`

	JoinButtonTexts  []string `json:"joinButtonTexts"`
	NameInputLabels  []string `json:"nameInputLabels"`
	LeftMeetingTexts []string `json:"leftMeetingTexts"`

	// Чем Meet помечает говорящего прямо сейчас. Устойчивого признака у него
	// нет: подсветка рисуется элементом с обфусцированным классом, а такие
	// классы меняются от релиза к релизу. Поэтому список пустой по умолчанию —
	// скрипт сперва пробует общие признаки (атрибут про речь, подпись для
	// скринридера), а сюда кладут то, что видно на живом созвоне через дамп
	// `--debug-captions`. Правится без пересборки, как и всё остальное здесь.
	//
	// Селекторы ищутся ВНУТРИ плитки [data-participant-id]; плитка считается
	// говорящей, если найденный элемент виден.
	SpeakingSelectors []string `json:"speakingSelectors"`
	// jsname контейнера с полосками микрофона. Meet гонит прозрачность его
	// обёртки от 0 к 1, пока человек говорит. jsname генерируется Closure, но
	// живёт заметно дольше имён классов — на том же основании здесь уже есть
	// captionRegionJsnames.
	SpeakingJsnames []string `json:"speakingJsnames"`
	// Цвета, которыми Meet заливает подсветку говорящего, ровно в том виде, в
	// каком их отдаёт getComputedStyle. Последний рубеж: цвет меняется реже
	// имени класса, но всё-таки меняется — в ноябре 2025 менялся.
	SpeakingColors []string `json:"speakingColors"`
	// Подписи для скринридера, которыми площадка сообщает, что человек
	// говорит. Зависят от языка интерфейса — потому здесь.
	SpeakingLabels []string `json:"speakingLabels"`
	// Подписи панели «Участники» и кнопки, которая её открывает. Полоски
	// микрофона Meet рисует в строках этой панели, а плитки в сетке
	// виртуализируются — говорящего может не быть в сетке вовсе.
	PeoplePanelLabels []string `json:"peoplePanelLabels"`

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

func LoadSelectors(path string) (*Selectors, error) {
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
