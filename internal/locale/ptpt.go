package locale

import (
	"fmt"
	"time"
)

var months = [...]string{
	"",
	"janeiro",
	"fevereiro",
	"março",
	"abril",
	"maio",
	"junho",
	"julho",
	"agosto",
	"setembro",
	"outubro",
	"novembro",
	"dezembro",
}

func DateNumeric(value time.Time, location *time.Location) string {
	return value.In(location).Format("02/01/2006")
}

func DateLong(value time.Time, location *time.Location) string {
	local := value.In(location)
	return fmt.Sprintf("%d de %s de %d", local.Day(), months[local.Month()], local.Year())
}

func DateTime(value time.Time, location *time.Location) string {
	return value.In(location).Format("02/01/2006 15:04 MST")
}

func Role(value string) string {
	switch value {
	case "Admin":
		return "Administração"
	case "Competitor":
		return "Competição"
	case "Leisure":
		return "Lazer"
	case "Guardian":
		return "Encarregado de educação"
	default:
		return "Desconhecido"
	}
}

func Squad(value string) string {
	switch value {
	case "Iniciante":
		return "Iniciante"
	case "Polo_Senior":
		return "Polo sénior"
	case "Master_A":
		return "Master A"
	case "Lazer":
		return "Lazer"
	case "None":
		return "Sem categoria"
	default:
		return "Desconhecida"
	}
}

func EquipmentType(value string) string {
	switch value {
	case "Boat":
		return "Embarcação"
	case "Paddle":
		return "Pagaia"
	case "Vehicle":
		return "Viatura"
	default:
		return "Equipamento"
	}
}

func RepairStatus(value string) string {
	switch value {
	case "Pendente":
		return "Pendente"
	case "Em_Analise":
		return "Em análise"
	case "Resolvido":
		return "Resolvido"
	default:
		return "Desconhecido"
	}
}
