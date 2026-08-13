package featureflags

type Key string

const (
	Suggestions                Key = "suggestions"
	PhotoSubmissions           Key = "photo_submissions"
	StructuredTrainingPlanning Key = "structured_training_planning"
)

type Mode string

const (
	Disabled  Mode = "DISABLED"
	AdminOnly Mode = "ADMIN_ONLY"
	Enabled   Mode = "ENABLED"
)

type Definition struct {
	Key         Key
	Label       string
	Description string
	Default     Mode
}

var registry = []Definition{
	{
		Key:         Suggestions,
		Label:       "Sugestões",
		Description: "Envio de sugestões pelos membros e respetiva gestão pela equipa autorizada.",
		Default:     Enabled,
	},
	{
		Key:         PhotoSubmissions,
		Label:       "Envio de fotografias para álbuns",
		Description: "Envio privado de fotografias para moderação antes de qualquer publicação no álbum.",
		Default:     Disabled,
	},
	{
		Key:         StructuredTrainingPlanning,
		Label:       "Planeamento estruturado de treinos",
		Description: "Semanas, grupos de treino e sessões híbridas durante a transição do planeamento atual.",
		Default:     AdminOnly,
	},
}

func Registry() []Definition {
	result := make([]Definition, len(registry))
	copy(result, registry)
	return result
}

func DefinitionFor(key Key) (Definition, bool) {
	for _, definition := range registry {
		if definition.Key == key {
			return definition, true
		}
	}
	return Definition{}, false
}

func ValidMode(mode Mode) bool {
	return mode == Disabled || mode == AdminOnly || mode == Enabled
}

func EffectiveMode(modes map[Key]Mode, key Key) (Mode, bool) {
	definition, known := DefinitionFor(key)
	if !known {
		return Disabled, false
	}
	mode, present := modes[key]
	if !present {
		return definition.Default, true
	}
	if !ValidMode(mode) {
		return Disabled, false
	}
	return mode, true
}

func Available(modes map[Key]Mode, key Key, administrator bool) bool {
	mode, valid := EffectiveMode(modes, key)
	if !valid {
		return false
	}
	switch mode {
	case Enabled:
		return true
	case AdminOnly:
		return administrator
	default:
		return false
	}
}
