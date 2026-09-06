package handlers

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrProfileForbidden      = errors.New("profile access forbidden")
	ErrProfileConflict       = errors.New("profile was changed")
	ErrConsentRequired       = errors.New("current image consent required")
	ErrHealthConsentRequired = errors.New("current health-data consent required")
)

type ProfileStore interface {
	View(context.Context, uuid.UUID, uuid.UUID, bool) (dbgen.GetMemberProfileRow, error)
	Update(context.Context, ProfileUpdate) error
	SavePhoto(context.Context, ProfilePhotoUpdate) (*string, error)
	RemovePhoto(context.Context, uuid.UUID, uuid.UUID, bool) (*string, error)
	Avatar(context.Context, dbgen.GetMemberAvatarParams) (dbgen.GetMemberAvatarRow, error)
}

type ProfileUpdate struct {
	ActorID, SubjectID          uuid.UUID
	IsAdmin                     bool
	Profile                     dbgen.UpdateMemberProfileParams
	Identity                    *dbgen.UpdateMemberIdentityParams
	ChangedFields               []string
	IdentityFields              []string
	HealthVersion, HealthSHA256 string
	AcceptHealthConsent         bool
	IP                          *netip.Addr
	UserAgent                   string
}

type ProfilePhotoUpdate struct {
	ActorID, SubjectID uuid.UUID
	IsAdmin            bool
	ObjectKey          string
	ContentType        string
	Size               int64
	ConsentVersion     string
	ConsentSHA256      string
	AcceptConsent      bool
	IP                 *netip.Addr
	UserAgent          string
}

type PostgresProfileStore struct {
	Pool *pgxpool.Pool
	DB   profileDB
	Now  func() time.Time
}

type profileDB interface {
	db.Beginner
	dbgen.DBTX
}

func (s PostgresProfileStore) database() profileDB {
	if s.DB != nil {
		return s.DB
	}
	if s.Pool == nil {
		return nil
	}
	return s.Pool
}

func (s PostgresProfileStore) View(ctx context.Context, actorID, subjectID uuid.UUID, isAdmin bool) (result dbgen.GetMemberProfileRow, err error) {
	err = db.WithinTx(ctx, s.database(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.EnsureMemberProfile(ctx, subjectID); err != nil {
			return err
		}
		result, err = q.GetMemberProfile(ctx, subjectID)
		if err != nil {
			return err
		}
		if !canViewProfile(result, actorID, isAdmin) {
			return pgx.ErrNoRows
		}
		_, err = q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: actorID, SubjectUserID: subjectID, Action: "SENSITIVE_VIEW", ChangedFields: []string{}})
		return err
	})
	return result, err
}

func (s PostgresProfileStore) Update(ctx context.Context, input ProfileUpdate) error {
	return db.WithinTx(ctx, s.database(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.EnsureMemberProfile(ctx, input.SubjectID); err != nil {
			return err
		}
		current, err := q.GetMemberProfile(ctx, input.SubjectID)
		if err != nil {
			return err
		}
		if !canEditProfile(current, input.ActorID, input.IsAdmin) {
			return ErrProfileForbidden
		}
		input.Profile.UserID = input.SubjectID
		if !input.IsAdmin {
			input.Profile.ClubMemberNumber = current.ClubMemberNumber
			input.Profile.FederationLicenceNumber = current.FederationLicenceNumber
			input.Identity = nil
		}
		if !input.IsAdmin && input.ActorID != input.SubjectID {
			input.Profile.MedicalDeclaration = current.MedicalDeclaration
			input.Profile.Allergies = current.Allergies
			input.Profile.MedicalConditions = current.MedicalConditions
			input.Profile.Medication = current.Medication
			input.Profile.ActivityRestrictions = current.ActivityRestrictions
			input.Profile.MedicalNotes = current.MedicalNotes
			input.ChangedFields = slices.DeleteFunc(input.ChangedFields, isHealthField)
		}
		if healthDataExpanded(current, input.Profile) {
			if input.IsAdmin || input.ActorID != input.SubjectID {
				return ErrHealthConsentRequired
			}
			currentConsent := false
			if profileRecordHasHealthData(current) {
				currentConsent, err = q.HasConsentVersion(ctx, dbgen.HasConsentVersionParams{UserID: input.SubjectID, ConsentType: "Dados_Saude", DocumentVersion: input.HealthVersion, DocumentSha256: input.HealthSHA256})
				if err != nil {
					return err
				}
			}
			if !currentConsent {
				if !input.AcceptHealthConsent {
					return ErrHealthConsentRequired
				}
				grantedBy := input.ActorID
				if _, err := q.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{UserID: input.SubjectID, GrantedByUserID: &grantedBy, ConsentType: "Dados_Saude", DocumentVersion: input.HealthVersion, DocumentSha256: input.HealthSHA256, IpAddress: input.IP, UserAgent: input.UserAgent}); err != nil {
					return err
				}
			}
		}
		if input.Identity != nil {
			emailChanged := input.Identity.Email != nil && (current.Email == nil || !strings.EqualFold(*current.Email, *input.Identity.Email))
			input.Identity.UserID = input.SubjectID
			if _, err := q.UpdateMemberIdentity(ctx, *input.Identity); errors.Is(err, pgx.ErrNoRows) {
				return ErrProfileConflict
			} else if err != nil {
				return err
			}
			if _, err := q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: input.ActorID, SubjectUserID: input.SubjectID, Action: "IDENTITY_UPDATED", ChangedFields: input.IdentityFields}); err != nil {
				return err
			}
			if emailChanged {
				if _, err := (emailverification.Service{Store: q, Now: s.Now}).Issue(ctx, input.SubjectID, *input.Identity.Email, false); err != nil {
					return err
				}
			}
		}
		if _, err := q.UpdateMemberProfile(ctx, input.Profile); errors.Is(err, pgx.ErrNoRows) {
			return ErrProfileConflict
		} else if err != nil {
			return err
		}
		_, err = q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: input.ActorID, SubjectUserID: input.SubjectID, Action: "PROFILE_UPDATED", ChangedFields: input.ChangedFields})
		return err
	})
}

func profileHasHealthData(profile dbgen.UpdateMemberProfileParams) bool {
	return profile.MedicalDeclaration == "NONE_KNOWN" || profile.MedicalDeclaration == "PROVIDED" || profile.Allergies != "" || profile.MedicalConditions != "" || profile.Medication != "" || profile.ActivityRestrictions != "" || profile.MedicalNotes != ""
}

func profileRecordHasHealthData(profile dbgen.GetMemberProfileRow) bool {
	return profile.MedicalDeclaration == "NONE_KNOWN" || profile.MedicalDeclaration == "PROVIDED" || profile.Allergies != "" || profile.MedicalConditions != "" || profile.Medication != "" || profile.ActivityRestrictions != "" || profile.MedicalNotes != ""
}

func healthDataExpanded(current dbgen.GetMemberProfileRow, next dbgen.UpdateMemberProfileParams) bool {
	if !profileHasHealthData(next) {
		return false
	}
	pairs := [][2]string{
		{current.Allergies, next.Allergies},
		{current.MedicalConditions, next.MedicalConditions},
		{current.Medication, next.Medication},
		{current.ActivityRestrictions, next.ActivityRestrictions},
		{current.MedicalNotes, next.MedicalNotes},
	}
	for _, pair := range pairs {
		if pair[1] != "" && pair[1] != pair[0] {
			return true
		}
	}
	return current.MedicalDeclaration != next.MedicalDeclaration && (next.MedicalDeclaration == "NONE_KNOWN" || next.MedicalDeclaration == "PROVIDED")
}

func isHealthField(field string) bool {
	switch field {
	case "medical_declaration", "allergies", "medical_conditions", "medication", "activity_restrictions", "medical_notes":
		return true
	default:
		return false
	}
}

func (s PostgresProfileStore) SavePhoto(ctx context.Context, input ProfilePhotoUpdate) (oldKey *string, err error) {
	err = db.WithinTx(ctx, s.database(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.EnsureMemberProfile(ctx, input.SubjectID); err != nil {
			return err
		}
		current, err := q.GetMemberProfile(ctx, input.SubjectID)
		if err != nil {
			return err
		}
		if !canEditProfile(current, input.ActorID, input.IsAdmin) {
			return ErrProfileForbidden
		}
		if input.IsAdmin || input.ActorID != input.SubjectID || !input.AcceptConsent {
			return ErrConsentRequired
		}
		grantedBy := input.ActorID
		consent, err := q.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{UserID: input.SubjectID, GrantedByUserID: &grantedBy, ConsentType: "Foto_Perfil", DocumentVersion: input.ConsentVersion, DocumentSha256: input.ConsentSHA256, IpAddress: input.IP, UserAgent: input.UserAgent})
		if err != nil {
			return err
		}
		consentID := consent.ID
		oldKey = current.PhotoObjectKey
		key, contentType, size := input.ObjectKey, input.ContentType, input.Size
		if _, err := q.UpdateMemberProfilePhoto(ctx, dbgen.UpdateMemberProfilePhotoParams{PhotoObjectKey: &key, PhotoContentType: &contentType, PhotoSizeBytes: &size, PhotoConsentFormID: &consentID, UserID: input.SubjectID}); err != nil {
			return err
		}
		action := "PHOTO_UPLOADED"
		if oldKey != nil {
			action = "PHOTO_REPLACED"
		}
		_, err = q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: input.ActorID, SubjectUserID: input.SubjectID, Action: action, ChangedFields: []string{"photo"}})
		return err
	})
	return oldKey, err
}

func (s PostgresProfileStore) RemovePhoto(ctx context.Context, actorID, subjectID uuid.UUID, isAdmin bool) (oldKey *string, err error) {
	err = db.WithinTx(ctx, s.database(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		current, err := q.GetMemberProfile(ctx, subjectID)
		if err != nil {
			return err
		}
		if !canEditProfile(current, actorID, isAdmin) {
			return ErrProfileForbidden
		}
		if current.PhotoObjectKey == nil {
			return pgx.ErrNoRows
		}
		oldKey = current.PhotoObjectKey
		if _, err := q.ClearMemberProfilePhoto(ctx, subjectID); err != nil {
			return err
		}
		_, err = q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: actorID, SubjectUserID: subjectID, Action: "PHOTO_REMOVED", ChangedFields: []string{"photo"}})
		return err
	})
	return oldKey, err
}

func (s PostgresProfileStore) Avatar(ctx context.Context, params dbgen.GetMemberAvatarParams) (dbgen.GetMemberAvatarRow, error) {
	return dbgen.New(s.database()).GetMemberAvatar(ctx, params)
}

func canViewProfile(profile dbgen.GetMemberProfileRow, actorID uuid.UUID, isAdmin bool) bool {
	return isAdmin || (profile.IsActive && (profile.ID == actorID || (profile.GuardianID != nil && *profile.GuardianID == actorID && profileIsMinor(profile))))
}

func canEditProfile(profile dbgen.GetMemberProfileRow, actorID uuid.UUID, isAdmin bool) bool {
	return isAdmin || (profile.IsActive && ((!profile.IsDependent && profile.ID == actorID) || (profile.GuardianID != nil && *profile.GuardianID == actorID && profileIsMinor(profile))))
}

func profileIsMinor(profile dbgen.GetMemberProfileRow) bool {
	if !profile.DateOfBirth.Valid {
		return false
	}
	today := time.Now().UTC()
	eighteenthBirthday := profile.DateOfBirth.Time.AddDate(18, 0, 0)
	return today.Before(eighteenthBirthday)
}
