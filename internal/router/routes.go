package router

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

var ErrModelNotFound = errors.New("model not found")

type Target struct {
	Provider string
	Model    string
}

func (t Target) String() string {
	return t.Provider + "/" + t.Model
}

type Route struct {
	Requested string
	Targets   []Target
}

type Model struct {
	ID      string
	OwnedBy string
}

type table struct {
	aliases map[string][]Target
	direct  map[string]Target
	bare    map[string][]Target
	models  []Model
}

func newTable(cfg config.Routes, providers map[string]provider.Provider) (table, error) {
	t := table{aliases: map[string][]Target{}, direct: map[string]Target{}, bare: map[string][]Target{}}
	var errs []error
	parse := func(raw string) Target {
		providerName, model, _ := strings.Cut(raw, "/")
		if _, ok := providers[providerName]; !ok {
			errs = append(errs, fmt.Errorf("route target %q references unavailable provider %q", raw, providerName))
		}
		return Target{Provider: providerName, Model: model}
	}
	aliasNames := make([]string, 0, len(cfg.Aliases))
	for alias := range cfg.Aliases {
		aliasNames = append(aliasNames, alias)
	}
	sort.Strings(aliasNames)
	for _, alias := range aliasNames {
		for _, raw := range cfg.Aliases[alias] {
			t.aliases[alias] = append(t.aliases[alias], parse(raw))
		}
		t.models = append(t.models, Model{ID: alias, OwnedBy: "gateway"})
	}
	for _, raw := range cfg.Direct {
		target := parse(raw)
		t.direct[target.String()] = target
		t.bare[target.Model] = append(t.bare[target.Model], target)
		t.models = append(t.models, Model{ID: target.String(), OwnedBy: target.Provider})
	}
	return t, errors.Join(errs...)
}

func (t table) resolve(model string) (Route, error) {
	if chain, ok := t.aliases[model]; ok {
		return Route{Requested: model, Targets: chain}, nil
	}
	if target, ok := t.direct[model]; ok {
		return Route{Requested: model, Targets: []Target{target}}, nil
	}
	if matches := t.bare[model]; len(matches) == 1 {
		return Route{Requested: model, Targets: matches}, nil
	}
	return Route{}, fmt.Errorf("%w: %q", ErrModelNotFound, model)
}
