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
