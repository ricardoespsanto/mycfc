package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

var errPublicationRhythmReference = errors.New("Escolha um perfil de ritmo ativo e reveja a publicação.")
var errPublicationRhythmPermission = errors.New("O perfil escolhido não corresponde às modalidades ou embarcações permitidas para um atleta. Indique um perfil permitido na adaptação individual ou da tripulação.")
var errPublicationRhythmZone = errors.New("Falta uma zona de ritmo no perfil selecionado. Complete a referência e reveja a publicação.")

type publicationRhythmCraft struct {
	SubjectID uuid.UUID `json:"subject_id"`
	Kayak     bool      `json:"kayak"`
	Canoe     bool      `json:"canoe"`
}
type publicationRhythmFacts struct {
	Profiles []dbgen.WaterIntensityProfile `json:"profiles"`
	Zones    []dbgen.WaterIntensityZone    `json:"zones"`
	Crafts   []publicationRhythmCraft      `json:"crafts"`
}

func canonicalRhythmProfileID(raw string) (uuid.UUID, error) {
	id, e := uuid.Parse(raw)
	if e != nil || id == uuid.Nil || id.String() != raw {
		return uuid.Nil, errPublicationRhythmReference
	}
	return id, nil
}
func publicationRhythmProfileIDs(s publicationSourceEnvelope) ([]uuid.UUID, error) {
	ids := map[uuid.UUID]bool{}
	for _, r := range s.Overview {
		if r.WaterIntensityProfileID != nil {
			if *r.WaterIntensityProfileID == uuid.Nil {
				return nil, errPublicationRhythmReference
			}
			ids[*r.WaterIntensityProfileID] = true
		}
	}
	for _, r := range s.VariationDefinitions {
		var patch map[string]json.RawMessage
		if json.Unmarshal(r.Patch, &patch) != nil {
			return nil, errPublicationSourceUnavailable
		}
		if raw, exists := patch["intensity_profile_id"]; exists {
			if r.SubjectKind != dbgen.TrainingVariationSubjectKindBLOCK || r.Operation != dbgen.TrainingVariationOperationOVERRIDE {
				return nil, errPublicationRhythmReference
			}
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return nil, errPublicationRhythmReference
			}
			id, e := canonicalRhythmProfileID(text)
			if e != nil {
				return nil, e
			}
			ids[id] = true
		}
	}
	if len(ids) > 32 {
		return nil, errTrainingPublicationReviewLimit
	}
	out := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out, nil
}
func validatePublicationRhythmFacts(s publicationSourceEnvelope, f publicationRhythmFacts) error {
	ids, e := publicationRhythmProfileIDs(s)
	if e != nil {
		return e
	}
	if len(f.Profiles) > 32 || len(f.Zones) > 256 || len(f.Crafts) > trainingPublicationReviewMaxTuples {
		return errTrainingPublicationReviewLimit
	}
	if len(ids) != len(f.Profiles) {
		return errPublicationRhythmReference
	}
	selected := map[uuid.UUID]bool{}
	for i, p := range f.Profiles {
		if p.ID != ids[i] || p.ID == uuid.Nil || !p.IsActive || p.Revision < 1 || strings.TrimSpace(p.Name) == "" || (p.Craft != dbgen.PaddlingCraftKAYAK && p.Craft != dbgen.PaddlingCraftCANOE) {
			return errPublicationRhythmReference
		}
		selected[p.ID] = true
	}
	zones, codes := map[uuid.UUID]bool{}, map[string]bool{}
	previous := ""
	for _, z := range f.Zones {
		key := z.ProfileID.String() + ":" + fmt.Sprintf("%010d", z.Position) + ":" + z.ID.String()
		if !selected[z.ProfileID] || z.ID == uuid.Nil || zones[z.ID] || codes[z.ProfileID.String()+":"+z.Code] || z.Position < 1 || z.Code == "" || z.Code != strings.TrimSpace(z.Code) || strings.TrimSpace(z.Label) == "" || strings.TrimSpace(z.Meaning) == "" || previous != "" && key <= previous {
			return errPublicationRhythmReference
		}
		if z.CadenceMin != nil && (*z.CadenceMin < 0 || *z.CadenceMin > 300) || z.CadenceMax != nil && (*z.CadenceMax < 0 || *z.CadenceMax > 300) || z.CadenceMin != nil && z.CadenceMax != nil && *z.CadenceMin > *z.CadenceMax {
			return errPublicationRhythmReference
		}
		zones[z.ID] = true
		codes[z.ProfileID.String()+":"+z.Code] = true
		previous = key
	}
	wanted := map[uuid.UUID]bool{}
	for _, c := range s.Candidates {
		wanted[c.SubjectID] = true
	}
	seen := map[uuid.UUID]bool{}
	var prior uuid.UUID
	for i, c := range f.Crafts {
		if c.SubjectID == uuid.Nil || !wanted[c.SubjectID] || seen[c.SubjectID] || i > 0 && bytes.Compare(prior[:], c.SubjectID[:]) >= 0 {
			return errPublicationSourceUnavailable
		}
		seen[c.SubjectID] = true
		prior = c.SubjectID
	}
	if len(seen) != len(wanted) {
		return errPublicationSourceUnavailable
	}
	return nil
}
func publicationRhythmDigest(f publicationRhythmFacts) ([32]byte, error) {
	b, e := json.Marshal(f)
	if e != nil {
		return [32]byte{}, e
	}
	if len(b) > 1<<20 {
		return [32]byte{}, errTrainingPublicationReviewLimit
	}
	return sha256.Sum256(b), nil
}
func rhythmProfileView(p dbgen.WaterIntensityProfile, zones []dbgen.WaterIntensityZone) pages.StructuredWaterProfile {
	out := pages.StructuredWaterProfile{ID: p.ID.String(), Name: p.Name, Craft: paddlingCraftLabel(string(p.Craft)), CraftCode: string(p.Craft), Notes: p.Notes, Revision: int(p.Revision), Zones: []pages.StructuredWaterZone{}}
	for _, z := range zones {
		if z.ProfileID != p.ID {
			continue
		}
		out.Zones = append(out.Zones, pages.StructuredWaterZone{ID: z.ID.String(), Position: int(z.Position), Code: z.Code, Label: z.Label, Cadence: rhythmZoneCadence(z), CadenceMin: z.CadenceMin, CadenceMax: z.CadenceMax, Meaning: z.Meaning})
	}
	return out
}
func rhythmCadence(low, high *int32) string {
	if low == nil && high == nil {
		return "Sem cadência fixa"
	}
	if low == nil {
		return fmt.Sprintf("—–%d remadas/min", *high)
	}
	if high == nil {
		return fmt.Sprintf("%d–— remadas/min", *low)
	}
	return fmt.Sprintf("%d–%d remadas/min", *low, *high)
}
func applyPublicationRhythm(session *pages.StructuredTrainingSession, r dbgen.ListStructuredTrainingPublicationMembersRow, s publicationSourceEnvelope) error {
	if s.Rhythm == nil {
		return nil
	}
	profiles := map[string]dbgen.WaterIntensityProfile{}
	for _, p := range s.Rhythm.Profiles {
		profiles[p.ID.String()] = p
	}
	var permission *publicationRhythmCraft
	if r.AthleteUserID == nil {
		return errPublicationSourceUnavailable
	}
	for i := range s.Rhythm.Crafts {
		if s.Rhythm.Crafts[i].SubjectID == *r.AthleteUserID {
			permission = &s.Rhythm.Crafts[i]
			break
		}
	}
	if permission == nil {
		return errPublicationSourceUnavailable
	}
	bases := map[string]string{}
	codes := map[string]string{}
	cadences := map[string]*int32{}
	for _, raw := range s.Overview {
		if raw.BlockID != nil && raw.WaterIntensityProfileID != nil {
			bases[raw.BlockID.String()] = raw.WaterIntensityProfileID.String()
		}
		if raw.WaterStepID != nil {
			if raw.WaterStepIntensityCode != nil {
				codes[raw.WaterStepID.String()] = *raw.WaterStepIntensityCode
			}
			cadences[raw.WaterStepID.String()] = raw.WaterStepCadenceSpm
		}
	}
	for si := range session.Segments {
		for bi := range session.Segments[si].Blocks {
			block := &session.Segments[si].Blocks[bi]
			if block.WaterMethod == "" {
				if block.WaterReference != nil {
					return errPublicationRhythmReference
				}
				continue
			}
			id := bases[block.ID]
			if block.WaterReference != nil {
				id = block.WaterReference.ID
			}
			if id == "" {
				for _, step := range block.WaterSteps {
					if step.IntensityCode != "" || codes[step.ID] != "" {
						return errPublicationRhythmReference
					}
				}
				continue
			}
			profile, ok := profiles[id]
			if !ok {
				return errPublicationRhythmReference
			}
			if profile.Craft == dbgen.PaddlingCraftKAYAK && !permission.Kayak || profile.Craft == dbgen.PaddlingCraftCANOE && !permission.Canoe {
				return errPublicationRhythmPermission
			}
			view := rhythmProfileView(profile, s.Rhythm.Zones)
			block.WaterReference = &view
			block.WaterProfile = fmt.Sprintf("%s · %s · revisão %d", view.Name, view.Craft, view.Revision)
			zoneByCode := map[string]pages.StructuredWaterZone{}
			for _, z := range view.Zones {
				zoneByCode[z.Code] = z
			}
			for i := range block.WaterSteps {
				step := &block.WaterSteps[i]
				code := step.IntensityCode
				if code == "" {
					code = codes[step.ID]
				}
				step.IntensityCode = code
				step.CadenceSPM = cadences[step.ID]
				parts := []string{}
				if code != "" {
					parts = append(parts, code)
				}
				if cadence := cadences[step.ID]; cadence != nil {
					parts = append(parts, fmt.Sprintf("%d remadas/min", *cadence))
				}
				if code != "" {
					zone, ok := zoneByCode[code]
					if !ok {
						return errPublicationRhythmZone
					}
					parts = append(parts, zone.Label, "orientação "+zone.Cadence)
					if zone.Meaning != zone.Cadence {
						parts = append(parts, zone.Meaning)
					}
				}
				step.Intensity = strings.Join(parts, " · ")
			}
		}
	}
	return nil
}

func rhythmFocusedProfileReferences(f focusedGraphProfileFacts) map[string]pages.StructuredWaterProfile {
	out := map[string]pages.StructuredWaterProfile{}
	for _, p := range f.Profiles {
		out[p.ID.String()] = rhythmProfileView(p, f.Zones)
	}
	return out
}

func rhythmZoneCadence(z dbgen.WaterIntensityZone) string {
	if z.CadenceMin == nil || z.CadenceMax == nil {
		return z.Meaning
	}
	return rhythmCadence(z.CadenceMin, z.CadenceMax)
}

func attachFocusedRhythmReferences(session *pages.StructuredTrainingSession, p focusedWorkoutGraph) {
	selected := map[string]string{}
	for _, a := range p.Activities {
		for _, b := range a.Phases {
			if b.ID != uuid.Nil && b.Water != nil && b.Water.ProfileID != nil {
				selected[b.ID.String()] = b.Water.ProfileID.String()
			}
		}
	}
	refs := rhythmFocusedProfileReferences(p.References)
	for si := range session.Segments {
		for bi := range session.Segments[si].Blocks {
			block := &session.Segments[si].Blocks[bi]
			if ref, ok := refs[selected[block.ID]]; ok {
				block.WaterReference = &ref
			}
		}
	}
}
