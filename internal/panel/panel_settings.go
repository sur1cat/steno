package panel

import (
	"context"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Настройки в панели. Описать проект, приложить сайт и репозиторий, завести
// бота в рабочий Slack — работа того, кто в этом разбирается, а не того, кто
// умеет править JSON и перезапускать сервис.
//
// Секретов здесь нет вовсе. Не спрятаны за звёздочками, а не показываются
// совсем — вместе с именами переменных окружения, в которых они лежат. Токены
// задаёт `steno setup` тому, кто разворачивает сервис; тому, кто пришёл сюда
// работать, они не говорят ничего и починить он по ним ничего не может.

// buildContextInBackground собирает справку о проекте: это поход в Claude на
// несколько секунд, и держать на нём запрос незачем.
func (p *Panel) buildContextInBackground(pr core.Project) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	material, fp, err := brain.GatherSources(ctx, p.cfg.DataDir, pr)
	if err != nil {
		p.log.Printf(i18n.Tr("справка «%s»: %v"), pr.Name, err)
		return
	}
	primer, spend, err := brain.BuildPrimer(ctx, p.cfg, pr, material)
	if err != nil {
		p.log.Printf(i18n.Tr("справка «%s»: %v"), pr.Name, err)
		return
	}
	if err := p.st.SaveProjectContext(core.ProjectContext{
		Project: pr.Name, Primer: primer, Fingerprint: fp, Sources: core.SourcesSummary(pr),
	}); err != nil {
		p.log.Printf(i18n.Tr("справка «%s»: %v"), pr.Name, err)
		return
	}
	p.log.Printf(i18n.Tr("справка «%s» пересобрана, %s"), pr.Name, spend)
}
