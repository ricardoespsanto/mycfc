package handlers

import (
	"context"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type seasonSetupReader interface {
	SeasonSetupStatus(context.Context, uuid.UUID) (bool, bool, error)
}

func readSeasonSetupStatus(ctx context.Context, pool *pgxpool.Pool, actor uuid.UUID) (bool, bool, error) {
	row, err := dbgen.New(pool).SeasonSetupStatus(ctx, actor)
	return row.Missing != nil && *row.Missing && row.Allowed, row.Administrator, err
}
func (s PostgresDefinitionStore) SeasonSetupStatus(ctx context.Context, actor uuid.UUID) (bool, bool, error) {
	return readSeasonSetupStatus(ctx, s.Pool, actor)
}
func (s PostgresClassificationStore) SeasonSetupStatus(ctx context.Context, actor uuid.UUID) (bool, bool, error) {
	return readSeasonSetupStatus(ctx, s.Pool, actor)
}
func seasonMissingGuidance(ctx context.Context, store any, actor uuid.UUID, c SeasonReturnContext) (message, setup string, err error) {
	reader, ok := store.(seasonSetupReader)
	if !ok {
		return "", "", nil
	}
	missing, admin, err := reader.SeasonSetupStatus(ctx, actor)
	if err != nil || !missing {
		return "", "", err
	}
	if admin {
		return "Ainda não existe uma época atual selecionada. Crie uma época com datas explícitas e selecione-a antes de continuar.", seasonSetupURL(c), nil
	}
	return "Ainda não existe uma época atual selecionada. Peça a um administrador que configure e selecione a época; depois regresse a esta tarefa para rever os dados.", "", nil
}
