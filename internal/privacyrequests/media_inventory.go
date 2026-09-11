package privacyrequests

// mediaSourceContract is the closed cross-store inventory for media that can
// participate in privacy execution. The column names are intentionally kept in
// source so the PostgreSQL integration test can reconcile schema drift before
// an unclassified pointer reaches production.
type mediaSourceContract struct {
	SourceKind        string
	Category          string
	Table             string
	ReferenceColumn   string
	ObjectKeyColumn   string
	ContentTypeColumn string
	SizeColumn        string
	IntentColumn      string
	SubjectBinding    string
}

var mediaSourceContracts = []mediaSourceContract{
	{
		SourceKind: "MEMBER_PROFILE_PHOTO", Category: "profile-photo", Table: "member_profiles",
		ReferenceColumn: "user_id", ObjectKeyColumn: "photo_object_key", ContentTypeColumn: "photo_content_type",
		SizeColumn: "photo_size_bytes", IntentColumn: "photo_upload_intent_id", SubjectBinding: "SOURCE_REF",
	},
	{
		SourceKind: "REPAIR_ATTACHMENT", Category: "object-storage", Table: "repair_requests",
		ReferenceColumn: "id", ObjectKeyColumn: "image_object_key", ContentTypeColumn: "image_content_type",
		SizeColumn: "image_size_bytes", IntentColumn: "image_upload_intent_id", SubjectBinding: "INTENT_SUBJECT",
	},
	{
		SourceKind: "EQUIPMENT_PHOTO", Category: "object-storage", Table: "equipment",
		ReferenceColumn: "id", ObjectKeyColumn: "image_object_key", ContentTypeColumn: "image_content_type",
		SizeColumn: "image_size_bytes", IntentColumn: "image_upload_intent_id", SubjectBinding: "INTENT_PROVENANCE_ACTOR",
	},
}

func mediaSourceContractFor(sourceKind string) (mediaSourceContract, bool) {
	for _, contract := range mediaSourceContracts {
		if contract.SourceKind == sourceKind {
			return contract, true
		}
	}
	return mediaSourceContract{}, false
}
