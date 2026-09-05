// Package featureflags defines the shared flag registry and pure evaluation.
// All flags default to disabled. Runtime overrides are owned by management.
package featureflags

import "errors"

type Key string

const ScoreAwareRating Key = "rating.score_aware"

var ErrUnknown = errors.New("unknown feature flag")

type Definition struct {
	Key         Key    `json:"key"`
	Description string `json:"description"`
	Service     string `json:"service"`
	Default     bool   `json:"default"`
	GroupScoped bool   `json:"group_scoped"`
}

// Definitions returns a copy so callers cannot mutate the registry.
func Definitions() []Definition {
	return []Definition{{Key: ScoreAwareRating, Description: "Experimental score-weighted Glicko-2 for typed 1v1 results", Service: "management", Default: false, GroupScoped: true}}
}
func Lookup(key Key) (Definition, error) {
	for _, d := range Definitions() {
		if d.Key == key {
			return d, nil
		}
	}
	return Definition{}, ErrUnknown
}

type State struct {
	Definition
	Global   *bool  `json:"global"`
	Override *bool  `json:"override"`
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
}

func Resolve(d Definition, global, group *bool) State {
	s := State{Definition: d, Global: global, Override: group, Enabled: d.Default, Source: "default"}
	if global != nil {
		s.Enabled, s.Source = *global, "global"
	}
	if group != nil && d.GroupScoped {
		s.Enabled, s.Source = *group, "group"
	}
	return s
}
