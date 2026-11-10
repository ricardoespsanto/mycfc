package handlers

import (
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/cfcoimbra/mycfc/ui/pages"
)

var errFocusedArithmetic = errors.New("structured workload arithmetic exceeds supported limits")

func focusedCheckedMultiply(values ...int64) (int64, error) {
	out := int64(1)
	for _, n := range values {
		if n < 0 || n != 0 && out > math.MaxInt64/n {
			return 0, errFocusedArithmetic
		}
		out *= n
	}
	return out, nil
}
func focusedCheckedAdd(dst *int64, n int64) error {
	if n < 0 || *dst < 0 || *dst > math.MaxInt64-n {
		return errFocusedArithmetic
	}
	*dst += n
	return nil
}
func focusedMeasureAdd(t *waterMeasureTotal, value int64, factors ...int64) error {
	m := append([]int64{value}, factors...)
	n, err := focusedCheckedMultiply(m...)
	if err != nil {
		return err
	}
	if err = focusedCheckedAdd(&t.value, n); err != nil {
		return err
	}
	t.hasValue = true
	return nil
}

// This is the canonical water arithmetic used both before accepting a new graph
// and by existing instruction summaries. Every leaf, recovery and intensity
// contribution is checked; malformed external graphs get unknown totals through
// the existing wrapper rather than wrapped numeric claims.
func calculateWaterTotalsChecked(steps []pages.StructuredWaterStep) ([]pages.StructuredWaterTotal, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	children := map[string][]pages.StructuredWaterStep{}
	byID := map[string]pages.StructuredWaterStep{}
	for _, s := range steps {
		if s.ID == "" || byID[s.ID].ID != "" || s.DurationSeconds < 0 || s.DistanceMetres < 0 || s.Recovery < 0 {
			return nil, errFocusedArithmetic
		}
		byID[s.ID] = s
		children[s.ParentID] = append(children[s.ParentID], s)
	}
	for _, s := range steps {
		if s.ParentID != "" {
			if parent, ok := byID[s.ParentID]; !ok || parent.Kind != "REPEAT_GROUP" {
				return nil, errFocusedArithmetic
			}
		}
	}
	duration, distance, recovery := waterMeasureTotal{}, waterMeasureTotal{}, waterMeasureTotal{hasValue: true}
	intensity := map[string]*waterMeasureTotal{}
	visited := map[string]bool{}
	var walk func(string, int64, int) error
	walk = func(parent string, multiplier int64, depth int) error {
		if depth > focusedMaxWaterDepth {
			return errFocusedArithmetic
		}
		for _, s := range children[parent] {
			if visited[s.ID] {
				return errFocusedArithmetic
			}
			visited[s.ID] = true
			switch s.Kind {
			case "REPEAT_GROUP":
				if s.Repeats < 1 || s.DurationSeconds != 0 || s.DistanceMetres != 0 || len(children[s.ID]) == 0 {
					return errFocusedArithmetic
				}
				next, err := focusedCheckedMultiply(multiplier, int64(s.Repeats))
				if err != nil {
					return err
				}
				if err = walk(s.ID, next, depth+1); err != nil {
					return err
				}
				if s.Recovery > 0 && s.Repeats > 1 {
					if err = focusedMeasureAdd(&recovery, int64(s.Recovery), int64(s.Repeats-1), multiplier); err != nil {
						return err
					}
				}
			case "EFFORT":
				if s.Repeats != 0 || len(children[s.ID]) > 0 {
					return errFocusedArithmetic
				}
				if s.DurationSeconds > 0 {
					if err := focusedMeasureAdd(&duration, int64(s.DurationSeconds), multiplier); err != nil {
						return err
					}
					duration.estimated = duration.estimated || s.DurationCertainty == "ESTIMATED"
				} else {
					duration.unknown = true
				}
				if s.DistanceMetres > 0 {
					if err := focusedMeasureAdd(&distance, int64(s.DistanceMetres), multiplier); err != nil {
						return err
					}
					distance.estimated = distance.estimated || s.DistanceCertainty == "ESTIMATED"
				} else {
					distance.unknown = true
				}
				if s.Recovery > 0 {
					if err := focusedMeasureAdd(&recovery, int64(s.Recovery), multiplier); err != nil {
						return err
					}
				}
				if code := strings.TrimSpace(strings.Split(s.Intensity, " · ")[0]); code != "" {
					total := intensity[code]
					if total == nil {
						total = &waterMeasureTotal{}
						intensity[code] = total
					}
					if s.DurationSeconds > 0 {
						if err := focusedMeasureAdd(total, int64(s.DurationSeconds), multiplier); err != nil {
							return err
						}
						total.estimated = total.estimated || s.DurationCertainty == "ESTIMATED"
					} else {
						total.unknown = true
					}
				}
			default:
				return errFocusedArithmetic
			}
		}
		return nil
	}
	if err := walk("", 1, 1); err != nil {
		return nil, err
	}
	if len(visited) != len(steps) {
		return nil, errFocusedArithmetic
	}
	// A later combined effort+recovery summary must be representable too.
	combined := duration.value
	if err := focusedCheckedAdd(&combined, recovery.value); err != nil {
		return nil, err
	}
	totals := []pages.StructuredWaterTotal{waterTotalView("Esforço planeado", duration, formatTrainingDuration), waterTotalView("Recuperação planeada", recovery, formatTrainingDuration), waterTotalView("Distância planeada", distance, func(n int64) string { return formatKilometres(n) })}
	codes := []string{}
	for code := range intensity {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		totals = append(totals, waterTotalView("Tempo em "+code, *intensity[code], formatTrainingDuration))
	}
	return totals, nil
}
func focusedGraphCheckedTotals(p focusedWorkoutGraph) ([]pages.StructuredWaterTotal, error) {
	steps := []pages.StructuredWaterStep{}
	gymDuration, gymDistance, gymRecovery := int64(0), int64(0), int64(0)
	for _, a := range p.Activities {
		for _, phase := range a.Phases {
			if w := phase.Water; w != nil {
				for _, n := range w.Nodes {
					s := pages.StructuredWaterStep{ID: focusedNodePrefix(a.Key, phase.Key, n.Key), Kind: string(n.Kind), Name: n.Name}
					if n.ParentKey != "" {
						s.ParentID = focusedNodePrefix(a.Key, phase.Key, n.ParentKey)
					}
					if n.Repeats != nil {
						s.Repeats = int(*n.Repeats)
					}
					if n.Duration != nil {
						s.DurationSeconds = int(*n.Duration)
					}
					if n.DurationCertainty != nil {
						s.DurationCertainty = string(*n.DurationCertainty)
					}
					if n.Distance != nil {
						s.DistanceMetres = int(*n.Distance)
					}
					if n.DistanceCertainty != nil {
						s.DistanceCertainty = string(*n.DistanceCertainty)
					}
					if n.Recovery != nil {
						s.Recovery = int(*n.Recovery)
					}
					if n.Intensity != nil {
						s.Intensity = *n.Intensity
					}
					steps = append(steps, s)
				}
			}
			if g := phase.Gym; g != nil {
				for _, x := range g.Exercises {
					factors := []int64{int64(g.Rounds)}
					if x.Sets != nil {
						factors = append(factors, int64(*x.Sets))
					}
					m, err := focusedCheckedMultiply(factors...)
					if err != nil {
						return nil, err
					}
					for _, measure := range []struct {
						value *int32
						total *int64
					}{{x.Duration, &gymDuration}, {x.Distance, &gymDistance}, {x.Recovery, &gymRecovery}} {
						value, total := measure.value, measure.total
						if value != nil {
							n, err := focusedCheckedMultiply(int64(*value), m)
							if err != nil {
								return nil, err
							}
							if err = focusedCheckedAdd(total, n); err != nil {
								return nil, err
							}
						}
					}
					if x.Repetitions != nil {
						if _, err = focusedCheckedMultiply(int64(*x.Repetitions), m); err != nil {
							return nil, err
						}
					}
				}
				if g.RoundRecovery != nil && g.Rounds > 1 {
					n, err := focusedCheckedMultiply(int64(*g.RoundRecovery), int64(g.Rounds-1))
					if err != nil {
						return nil, err
					}
					if err = focusedCheckedAdd(&gymRecovery, n); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if err := focusedCheckedAdd(&gymDuration, gymRecovery); err != nil {
		return nil, err
	}
	return calculateWaterTotalsChecked(steps)
}
