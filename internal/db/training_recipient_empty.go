package db

import (
	"context"
	"errors"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
)

type RecipientBootstrapPhase string

const (
	RecipientBareEmpty     RecipientBootstrapPhase = "bare-empty"
	RecipientPreparedEmpty RecipientBootstrapPhase = "prepared-empty"
)

// InspectTrainingRecipientLocalBootstrapTarget is only a structural proof for
// the documented owned local/test bootstrap->migrate flow. Callers MUST also
// validate local/test environment and concrete owned target; this API does not
// infer host ownership from a database name. Stamped binaries cannot use it.
// No ledger/application rows, functions, or private state run as bootstrap.
// A persistent ledger, even apparently empty, is not a supported prepared phase.
func InspectTrainingRecipientLocalBootstrapTarget(ctx context.Context, q TrainingScheduleQueryReader, database, migrationRole, publisherRole string) (RecipientBootstrapPhase, error) {
	if releasecontract.Version != "" || releasecontract.Candidate != "" {
		return "", errors.New("local bootstrap inspection requires wholly unstamped binary")
	}
	if !postgresIdentifier.MatchString(database) || !postgresIdentifier.MatchString(migrationRole) || !postgresIdentifier.MatchString(publisherRole) || migrationRole == publisherRole {
		return "", errors.New("local bootstrap inspection identity invalid")
	}
	if empty, err := IsTrainingRecipientEmptyTarget(ctx, q, database); err != nil {
		return "", err
	} else if empty {
		return RecipientBareEmpty, nil
	}
	var prepared bool
	if err := q.QueryRow(ctx, recipientPreparedEmptySQL, database, migrationRole, publisherRole).Scan(&prepared); err != nil || !prepared {
		return "", errors.New("local bootstrap target is not a reviewed empty phase")
	}
	return RecipientPreparedEmpty, nil
}

const recipientPreparedEmptySQL = `SELECT /* training recipient prepared empty */
 pg_catalog.current_database() OPERATOR(pg_catalog.=) $1::pg_catalog.name AND session_user OPERATOR(pg_catalog.=) current_user
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_roles authenticated WHERE authenticated.rolname OPERATOR(pg_catalog.=) session_user AND (authenticated.rolsuper OR (authenticated.rolname OPERATOR(pg_catalog.=) migration.rolname AND NOT authenticated.rolcreaterole AND NOT authenticated.rolreplication AND NOT authenticated.rolbypassrls)))
 AND NOT migration.rolsuper AND NOT migration.rolcreaterole AND NOT migration.rolreplication AND NOT migration.rolbypassrls
 AND NOT publisher.rolsuper AND NOT publisher.rolcreaterole AND NOT publisher.rolreplication AND NOT publisher.rolbypassrls
 AND public_namespace.nspowner OPERATOR(pg_catalog.=) migration.oid AND metadata.nspowner OPERATOR(pg_catalog.=) migration.oid
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace namespace WHERE namespace.nspname OPERATOR(pg_catalog.<>) 'public'::pg_catalog.name AND namespace.nspname OPERATOR(pg_catalog.<>) 'mycfc_meta'::pg_catalog.name AND namespace.nspname OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.name AND namespace.nspname OPERATOR(pg_catalog.<>) 'information_schema'::pg_catalog.name AND namespace.nspname::pg_catalog.text OPERATOR(pg_catalog.!~~) 'pg_toast%'::pg_catalog.text AND namespace.nspname::pg_catalog.text OPERATOR(pg_catalog.!~~) 'pg_temp%'::pg_catalog.text)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_depend dependency WHERE dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_namespace'::pg_catalog.regclass AND dependency.refobjid OPERATOR(pg_catalog.=) metadata.oid)
 AND (SELECT pg_catalog.count(*) FROM pg_catalog.pg_extension extension JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) extension.extowner WHERE (extension.extname OPERATOR(pg_catalog.=) 'citext'::pg_catalog.name OR extension.extname OPERATOR(pg_catalog.=) 'pgcrypto'::pg_catalog.name) AND extension.extnamespace OPERATOR(pg_catalog.=) public_namespace.oid AND owner.rolsuper) OPERATOR(pg_catalog.=) 2::pg_catalog.int8
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_extension extension WHERE extension.extname OPERATOR(pg_catalog.<>) 'plpgsql'::pg_catalog.name AND extension.extname OPERATOR(pg_catalog.<>) 'citext'::pg_catalog.name AND extension.extname OPERATOR(pg_catalog.<>) 'pgcrypto'::pg_catalog.name)
 AND (SELECT pg_catalog.count(*) FROM pg_catalog.pg_default_acl default_acl WHERE default_acl.defaclnamespace OPERATOR(pg_catalog.=) public_namespace.oid) OPERATOR(pg_catalog.=) 2::pg_catalog.int8
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_depend object_dependency WHERE object_dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_namespace'::pg_catalog.regclass AND object_dependency.refobjid OPERATOR(pg_catalog.=) public_namespace.oid AND NOT (
  -- Exact defaults installed by BootstrapRoles, not arbitrary schema code.
  (object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_default_acl'::pg_catalog.regclass AND EXISTS(
   SELECT 1 FROM pg_catalog.pg_default_acl default_acl WHERE default_acl.oid OPERATOR(pg_catalog.=) object_dependency.objid AND default_acl.defaclrole OPERATOR(pg_catalog.=) migration.oid AND default_acl.defaclnamespace OPERATOR(pg_catalog.=) public_namespace.oid
   AND (default_acl.defaclobjtype OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" OR default_acl.defaclobjtype OPERATOR(pg_catalog.=) 'S'::pg_catalog."char")
   AND (SELECT pg_catalog.count(*) FROM pg_catalog.aclexplode(default_acl.defaclacl)) OPERATOR(pg_catalog.=) CASE WHEN default_acl.defaclobjtype OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" THEN 4::pg_catalog.int8 ELSE 2::pg_catalog.int8 END
   AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(default_acl.defaclacl) acl WHERE acl.is_grantable OR acl.grantor OPERATOR(pg_catalog.<>) migration.oid OR acl.grantee OPERATOR(pg_catalog.<>) publisher.oid OR NOT (
    (default_acl.defaclobjtype OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" AND acl.privilege_type OPERATOR(pg_catalog.=) ANY(ARRAY['SELECT','INSERT','UPDATE','DELETE']::pg_catalog.text[])) OR (default_acl.defaclobjtype OPERATOR(pg_catalog.=) 'S'::pg_catalog."char" AND acl.privilege_type OPERATOR(pg_catalog.=) ANY(ARRAY['USAGE','SELECT']::pg_catalog.text[]))
   ))))
  OR EXISTS(SELECT 1 FROM pg_catalog.pg_extension extension JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) extension.extowner
   WHERE extension.oid OPERATOR(pg_catalog.=) object_dependency.objid AND object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_extension'::pg_catalog.regclass AND extension.extnamespace OPERATOR(pg_catalog.=) public_namespace.oid AND owner.rolsuper AND (extension.extname OPERATOR(pg_catalog.=) 'citext'::pg_catalog.name OR extension.extname OPERATOR(pg_catalog.=) 'pgcrypto'::pg_catalog.name))
  OR EXISTS(SELECT 1 FROM pg_catalog.pg_depend member JOIN pg_catalog.pg_extension extension ON extension.oid OPERATOR(pg_catalog.=) member.refobjid JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) extension.extowner
   WHERE member.classid OPERATOR(pg_catalog.=) object_dependency.classid AND member.objid OPERATOR(pg_catalog.=) object_dependency.objid AND member.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_extension'::pg_catalog.regclass AND member.deptype OPERATOR(pg_catalog.=) 'e'::pg_catalog."char" AND extension.extnamespace OPERATOR(pg_catalog.=) public_namespace.oid AND owner.rolsuper AND (extension.extname OPERATOR(pg_catalog.=) 'citext'::pg_catalog.name OR extension.extname OPERATOR(pg_catalog.=) 'pgcrypto'::pg_catalog.name)
   AND extension.extowner OPERATOR(pg_catalog.=) CASE
    WHEN object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_proc'::pg_catalog.regclass THEN (SELECT proowner FROM pg_catalog.pg_proc WHERE oid OPERATOR(pg_catalog.=) object_dependency.objid)
    WHEN object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_type'::pg_catalog.regclass THEN (SELECT typowner FROM pg_catalog.pg_type WHERE oid OPERATOR(pg_catalog.=) object_dependency.objid)
    WHEN object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_operator'::pg_catalog.regclass THEN (SELECT oprowner FROM pg_catalog.pg_operator WHERE oid OPERATOR(pg_catalog.=) object_dependency.objid)
    WHEN object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_opclass'::pg_catalog.regclass THEN (SELECT opcowner FROM pg_catalog.pg_opclass WHERE oid OPERATOR(pg_catalog.=) object_dependency.objid)
    WHEN object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_opfamily'::pg_catalog.regclass THEN (SELECT opfowner FROM pg_catalog.pg_opfamily WHERE oid OPERATOR(pg_catalog.=) object_dependency.objid)
    ELSE 0::pg_catalog.oid END)
  OR (object_dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_type'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_type array_type JOIN pg_catalog.pg_type element_type ON element_type.oid OPERATOR(pg_catalog.=) array_type.typelem JOIN pg_catalog.pg_depend member ON member.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_type'::pg_catalog.regclass AND member.objid OPERATOR(pg_catalog.=) element_type.oid AND member.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_extension'::pg_catalog.regclass AND member.deptype OPERATOR(pg_catalog.=) 'e'::pg_catalog."char" JOIN pg_catalog.pg_extension extension ON extension.oid OPERATOR(pg_catalog.=) member.refobjid JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) extension.extowner
   WHERE array_type.oid OPERATOR(pg_catalog.=) object_dependency.objid AND array_type.typowner OPERATOR(pg_catalog.=) extension.extowner AND element_type.typarray OPERATOR(pg_catalog.=) array_type.oid AND array_type.typnamespace OPERATOR(pg_catalog.=) public_namespace.oid AND extension.extname OPERATOR(pg_catalog.=) 'citext'::pg_catalog.name AND owner.rolsuper))
 ))
 FROM pg_catalog.pg_roles migration CROSS JOIN pg_catalog.pg_roles publisher CROSS JOIN pg_catalog.pg_namespace public_namespace CROSS JOIN pg_catalog.pg_namespace metadata
 WHERE migration.rolname OPERATOR(pg_catalog.=) $2::pg_catalog.name AND publisher.rolname OPERATOR(pg_catalog.=) $3::pg_catalog.name AND public_namespace.nspname OPERATOR(pg_catalog.=) 'public'::pg_catalog.name AND metadata.nspname OPERATOR(pg_catalog.=) 'mycfc_meta'::pg_catalog.name`
