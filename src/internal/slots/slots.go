package slots

import (
	"fmt"
	"strings"
)

type Tier string

const (
	TierSingleton    Tier = "singleton"
	TierCollection   Tier = "collection"
	TierUnstructured Tier = "unstructured"
)

type Def struct {
	Slot         string
	Tier         Tier
	Description  string
	PreviousSlot string
}

var Catalog = []Def{
	{"identity.name", TierSingleton, "person's full name", ""},
	{"identity.age", TierSingleton, "person's age", ""},
	{"identity.pronouns", TierSingleton, "preferred pronouns", ""},
	{"location.current", TierSingleton, "current city/country of residence", "location.previous"},
	{"location.previous", TierSingleton, "previous location (auto-set on supersession)", ""},
	{"location.hometown", TierSingleton, "hometown", ""},
	{"employment.current_company", TierSingleton, "current employer", "employment.previous_company"},
	{"employment.current_role", TierSingleton, "current job title/role", ""},
	{"employment.previous_company", TierSingleton, "previous employer (auto-set)", ""},
	{"relationship.partner", TierSingleton, "romantic partner", ""},
	{"preference.response_style", TierSingleton, "preferred response style (concise, detailed, etc.)", ""},
	{"preference.communication_style", TierSingleton, "communication preference", ""},
	{"preference.diet", TierSingleton, "dietary preference (vegetarian, vegan, etc.)", ""},
	{"pet", TierCollection, "pet (entity_key = pet name)", ""},
	{"family_member", TierCollection, "family member (entity_key = relation or name)", ""},
	{"restriction.allergy", TierCollection, "food or other allergy (entity_key = allergen)", ""},
	{"skill.using", TierCollection, "technology or skill the person uses", ""},
	{"preference.food", TierCollection, "food preference (entity_key = food item)", ""},
	{"opinion.topic", TierCollection, "opinion on a topic (entity_key = topic)", ""},
	{"project.current", TierCollection, "current project (entity_key = project name)", ""},
	{"event.upcoming", TierCollection, "upcoming event (entity_key = event description)", ""},
}

var (
	byName          = map[string]Def{}
	singletonSlots  = map[string]bool{}
	collectionSlots = map[string]bool{}
	profileSlots    = map[string]bool{
		"identity.name":               true,
		"identity.age":                true,
		"identity.pronouns":           true,
		"location.current":            true,
		"location.previous":           true,
		"location.hometown":           true,
		"employment.current_company":  true,
		"employment.current_role":     true,
		"employment.previous_company": true,
		"relationship.partner":        true,
		"preference.response_style":   true,
		"preference.diet":             true,
	}
)

func init() {
	for _, s := range Catalog {
		byName[s.Slot] = s
		switch s.Tier {
		case TierSingleton:
			singletonSlots[s.Slot] = true
		case TierCollection:
			collectionSlots[s.Slot] = true
		}
	}
}

func IsSingleton(slot string) bool  { return singletonSlots[slot] }
func IsCollection(slot string) bool { return collectionSlots[slot] }
func IsProfile(slot string) bool    { return profileSlots[slot] }
func IsValid(slot string) bool {
	return byName[slot].Slot != "" || slot == "unstructured"
}

func GetPreviousSlot(slot string) (string, bool) {
	def, ok := byName[slot]
	if !ok || def.PreviousSlot == "" {
		return "", false
	}
	return def.PreviousSlot, true
}

func ListForPrompt() string {
	var sb strings.Builder
	for _, s := range Catalog {
		sb.WriteString(fmt.Sprintf("  %s (%s): %s\n", s.Slot, s.Tier, s.Description))
	}
	sb.WriteString("  unstructured (unstructured): anything that doesn't fit above\n")
	return sb.String()
}

func Normalize(slot string) string {
	if IsValid(slot) {
		return slot
	}
	return "unstructured"
}
