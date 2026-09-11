package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sur1cat/steno/internal/spec"
)

// ТЗ для модели — только чтение.
//
// Собрать ТЗ стоит денег, а запустить по нему агента — это право писать файлы
// на машине с сервисом. Ни того, ни другого модели, которая роется в созвонах,
// не даём: обе двери — панель с паролем, строка меню и терминал, за которыми
// стоит человек. Здесь она может прочитать, что уже написано, и ответить на
// «что мы решили делать с сапаром и чего для этого не хватает».

type listSpecsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project name or alias as listed by projects; omit for every project"`
}

type specRow struct {
	ID        string   `json:"id"`
	ItemID    string   `json:"item_id" jsonschema:"the task it was written for; see open_items"`
	Project   string   `json:"project"`
	Title     string   `json:"title,omitempty"`
	Status    string   `json:"status" jsonschema:"draft, rejected, running, done or failed"`
	Reject    string   `json:"reject,omitempty" jsonschema:"why the task was declined, when status is rejected"`
	Runnable  bool     `json:"runnable" jsonschema:"whether an agent may be started from this spec"`
	Blocked   []string `json:"blocked,omitempty" jsonschema:"why it may not — a spec with no open question, no place in the code, no steps"`
	Unknowns  int      `json:"unknowns" jsonschema:"how many open questions the spec names"`
	Branch    string   `json:"branch,omitempty" jsonschema:"the branch the agent produced, once it ran"`
	Repo      string   `json:"repo,omitempty"`
	RunError  string   `json:"run_error,omitempty"`
	WrittenAt string   `json:"written_at"`
	USD       float64  `json:"usd" jsonschema:"what writing it cost"`
}

type listSpecsResult struct {
	Project string    `json:"project,omitempty"`
	Specs   []specRow `json:"specs"`
}

func fromSpec(sp *spec.Spec) specRow {
	gate := sp.Gate()
	return specRow{
		ID: sp.ID, ItemID: sp.ItemID, Project: sp.Project, Title: sp.Title,
		Status: sp.Status, Reject: sp.Reject,
		Runnable: sp.Status != spec.StatusRejected && len(gate) == 0, Blocked: gate,
		Unknowns: len(sp.Unknowns), Branch: sp.Branch, Repo: sp.Repo, RunError: sp.RunError,
		WrittenAt: stamp(sp.CreatedAt), USD: sp.USD,
	}
}

func (s *server) listSpecs(_ context.Context, _ *sdk.CallToolRequest, in listSpecsArgs) (*sdk.CallToolResult, listSpecsResult, error) {
	project := ""
	if p := strings.TrimSpace(in.Project); p != "" {
		name, err := s.resolveProject(p)
		if err != nil {
			return nil, listSpecsResult{}, err
		}
		project = name
	}
	store, err := spec.Open(s.st)
	if err != nil {
		return nil, listSpecsResult{}, err
	}
	latest, err := store.Latest(project)
	if err != nil {
		return nil, listSpecsResult{}, err
	}
	out := listSpecsResult{Project: project, Specs: make([]specRow, 0, len(latest))}
	for _, sp := range latest {
		out.Specs = append(out.Specs, fromSpec(sp))
	}
	// Свежие сверху: спрашивают обычно про то, что написали только что.
	sort.Slice(out.Specs, func(i, j int) bool { return out.Specs[i].WrittenAt > out.Specs[j].WrittenAt })
	return nil, out, nil
}

type getSpecArgs struct {
	ID string `json:"id" jsonschema:"spec id from list_specs, like S-3f2a"`
}

type getSpecResult struct {
	Spec     specRow `json:"spec"`
	Markdown string  `json:"markdown" jsonschema:"the spec as written: what is known, where in the code, steps, checks, assumptions, what is missing"`
	Log      string  `json:"log,omitempty" jsonschema:"the tail of the agent's log, when it ran"`
}

func (s *server) getSpec(_ context.Context, _ *sdk.CallToolRequest, in getSpecArgs) (*sdk.CallToolResult, getSpecResult, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, getSpecResult{}, errors.New("id is required — ids come from list_specs")
	}
	store, err := spec.Open(s.st)
	if err != nil {
		return nil, getSpecResult{}, err
	}
	sp, err := store.Get(id)
	if err != nil {
		return nil, getSpecResult{}, fmt.Errorf("no spec with id %q — ids come from list_specs", id)
	}
	return nil, getSpecResult{Spec: fromSpec(sp), Markdown: sp.Render(), Log: sp.RunLog}, nil
}
