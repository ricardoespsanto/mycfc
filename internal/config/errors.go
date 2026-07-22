package config

import (
	"fmt"
	"sort"
	"strings"
)

type Problem struct {
	Field   string
	Message string
}

type Problems []Problem

func (p *Problems) Add(field, message string) {
	*p = append(*p, Problem{Field: field, Message: message})
}

func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].Field == p[j].Field {
			return p[i].Message < p[j].Message
		}
		return p[i].Field < p[j].Field
	})
	return p
}

func (p Problems) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, problem := range p {
		fmt.Fprintf(&b, "\n- %s: %s", problem.Field, problem.Message)
	}
	return b.String()
}
