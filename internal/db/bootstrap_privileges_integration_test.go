//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestProvisionGuardianActivationRoleHasOnlyFixedOperatorAPI(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	if err = ProvisionGuardianActivationRole(ctx, tx, conn.Config().Database, guardianActivationRole, "integration-only-password"); err != nil {
		t.Fatal(err)
	}

	var connect, operatorUsage, publicUsage, metadataUsage, tableRead, runtimeRead, status, preflight, enable, disable, helper, releaseBind bool
	if err = tx.QueryRow(ctx, `SELECT
		has_database_privilege($1,current_database(),'CONNECT'),
		has_schema_privilege($1,'guardian_ops','USAGE'),
		has_schema_privilege($1,'public','USAGE'),
		has_schema_privilege($1,'mycfc_meta','USAGE'),
		has_table_privilege($1,'guardian_authority_policy_approvals','SELECT'),
		has_table_privilege($1,'guardian_ops.runtime_release_binding','SELECT'),
		has_function_privilege($1,'guardian_ops.status(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.disable(uuid,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.schema_ready(text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.release_disable_and_bind(text,text,text)','EXECUTE')`, guardianActivationRole).
		Scan(&connect, &operatorUsage, &publicUsage, &metadataUsage, &tableRead, &runtimeRead, &status, &preflight, &enable, &disable, &helper, &releaseBind); err != nil {
		t.Fatal(err)
	}
	if !connect || !operatorUsage || publicUsage || metadataUsage || tableRead || runtimeRead || !status || !preflight || !enable || !disable || helper || releaseBind {
		t.Fatalf("operator boundary connect=%v ops=%v public=%v metadata=%v table=%v runtime=%v status=%v preflight=%v enable=%v disable=%v helper=%v release=%v",
			connect, operatorUsage, publicUsage, metadataUsage, tableRead, runtimeRead, status, preflight, enable, disable, helper, releaseBind)
	}
	var superuser, createDB, createRole, inherit, replication, bypassRLS bool
	if err = tx.QueryRow(ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolinherit,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=$1`, guardianActivationRole).
		Scan(&superuser, &createDB, &createRole, &inherit, &replication, &bypassRLS); err != nil {
		t.Fatal(err)
	}
	if superuser || createDB || createRole || inherit || replication || bypassRLS {
		t.Fatalf("operator role attributes super=%v createdb=%v createrole=%v inherit=%v replication=%v bypassrls=%v", superuser, createDB, createRole, inherit, replication, bypassRLS)
	}
}
