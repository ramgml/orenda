// Package bot — config-driven registry builder (Phase 10.2).
package bot

import (
	"fmt"
	"strings"
)

// ConfigSpec describes one bot entry from config.yaml.
type ConfigSpec struct {
	Type    string         `yaml:"type"` // console | vk | telegram | email | webhook
	Enabled bool           `yaml:"enabled"`
	Config  map[string]any `yaml:"config"`
}

// BuildFromConfig constructs bots from the config spec list and registers
// them in reg. Disabled entries are skipped. Unknown types return an
// error so config mistakes surface at startup.
func BuildFromConfig(specs []ConfigSpec, reg *Registry) error {
	for _, spec := range specs {
		if !spec.Enabled {
			continue
		}
		b, err := build(spec)
		if err != nil {
			return fmt.Errorf("bot %q: %w", spec.Type, err)
		}
		reg.Register(b)
	}
	return nil
}

// build constructs one bot from a spec.
func build(spec ConfigSpec) (Bot, error) {
	switch strings.ToLower(spec.Type) {
	case "console":
		return Console{Out: discardWriter{}}, nil
	case "webhook":
		return NewWebhook(str(spec.Config, "url"), str(spec.Config, "secret")), nil
	case "email":
		return NewEmail(
			str(spec.Config, "host"),
			str(spec.Config, "username"),
			str(spec.Config, "password"),
			str(spec.Config, "from"),
			boolVal(spec.Config, "tls"),
		), nil
	case "telegram":
		return NewTelegram(str(spec.Config, "token")), nil
	case "vk":
		return NewVK(str(spec.Config, "token"), int64Val(spec.Config, "group_id")), nil
	default:
		return nil, fmt.Errorf("unknown bot type")
	}
}

// discardWriter is a no-op writer so the console bot doesn't spam stderr
// when it isn't the operator's intent.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// helpers for type-erased config maps
func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func boolVal(m map[string]any, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}

func int64Val(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
