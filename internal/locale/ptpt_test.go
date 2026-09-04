package locale

import (
	"testing"
	"time"
)

func TestPortugueseFormattingAndOperationalLabels(t *testing.T) {
	value := time.Date(2026, time.September, 4, 13, 5, 0, 0, time.UTC)
	if DateNumeric(value, time.UTC) != "04/09/2026" || DateLong(value, time.UTC) != "4 de setembro de 2026" || DateTime(value, time.UTC) != "04/09/2026 13:05 UTC" {
		t.Fatal("date formats are not pt-PT")
	}
	for input, want := range map[string]string{"Boat": "Embarcação", "Paddle": "Pagaia", "Vehicle": "Viatura", "other": "Equipamento"} {
		if got := EquipmentType(input); got != want {
			t.Errorf("EquipmentType(%q)=%q", input, got)
		}
	}
	for input, want := range map[string]string{"Pendente": "Pendente", "Em_Analise": "Em análise", "Resolvido": "Resolvido", "other": "Desconhecido"} {
		if got := RepairStatus(input); got != want {
			t.Errorf("RepairStatus(%q)=%q", input, got)
		}
	}
}
