package pages

import "testing"

func TestTrainingPageActionsSeparateActivityFromCoordination(t *testing.T) {
	tests := []struct {
		name   string
		page   TrainingPage
		labels []string
		hrefs  []string
	}{
		{
			name:   "coach activity remains read only",
			page:   TrainingPage{CanManage: true, StructuredAvailable: true},
			labels: []string{"Planeamento semanal"},
			hrefs:  []string{"/treinos/estruturados"},
		},
		{
			name:   "coordination exposes authoring",
			page:   TrainingPage{Management: true, CanManage: true, StructuredAvailable: true},
			labels: []string{"Criar plano", "Criar sessão", "Planeamento semanal"},
			hrefs:  []string{"/admin/treinos/planos/criar", "/admin/treinos/sessoes/criar", "/admin/treinos/estruturados"},
		},
		{
			name: "activity without structured planning has no actions",
			page: TrainingPage{CanManage: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := trainingPageActions(test.page)
			if len(actions) != len(test.labels) {
				t.Fatalf("actions=%#v", actions)
			}
			for index := range actions {
				if actions[index].Label != test.labels[index] || actions[index].Href != test.hrefs[index] {
					t.Fatalf("action[%d]=%#v", index, actions[index])
				}
			}
		})
	}
}
