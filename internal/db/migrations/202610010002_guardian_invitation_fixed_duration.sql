-- Guardian invitations expire after exactly 720 elapsed hours, including DST.
-- Future issuance only: existing invitation deadlines and lifecycle are unchanged.
CREATE OR REPLACE FUNCTION guardian_authority_issue_invitation(p_actor_id uuid,p_email citext,p_token_digest bytea)
RETURNS SETOF guardian_authority_invitations LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE result guardian_authority_invitations%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 IF p_email IS NULL OR p_email::text<>lower(btrim(p_email::text)) OR char_length(p_email::text) NOT BETWEEN 3 AND 254 OR octet_length(p_token_digest)<>32 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_invitation_rejected'; END IF;
 INSERT INTO guardian_authority_invitations(invited_email,token_digest,issued_by,issued_at,expires_at)
 VALUES(p_email,p_token_digest,p_actor_id,now_at,now_at+interval '720 hours') RETURNING * INTO result;RETURN NEXT result;
END;$$;
