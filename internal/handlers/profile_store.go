package handlers

import (
	"context"
	"errors"
	"net/netip"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrProfileForbidden = errors.New("profile access forbidden")
	ErrProfileConflict  = errors.New("profile was changed")
	ErrConsentRequired  = errors.New("current image consent required")
)

type ProfileStore interface {
	View(context.Context, uuid.UUID, uuid.UUID, bool) (dbgen.GetMemberProfileRow, error)
	Update(context.Context, ProfileUpdate) error
	SavePhoto(context.Context, ProfilePhotoUpdate) (*string, error)
	RemovePhoto(context.Context, uuid.UUID, uuid.UUID, bool) (*string, error)
	Avatar(context.Context, dbgen.GetMemberAvatarParams) (dbgen.GetMemberAvatarRow, error)
}

type ProfileUpdate struct {
	ActorID, SubjectID uuid.UUID
	IsAdmin            bool
	Profile            dbgen.UpdateMemberProfileParams
	Identity           *dbgen.UpdateMemberIdentityParams
	ChangedFields      []string
	IdentityFields     []string
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

type PostgresProfileStore struct{ Pool *pgxpool.Pool }

func (s PostgresProfileStore) View(ctx context.Context, actorID, subjectID uuid.UUID, isAdmin bool) (result dbgen.GetMemberProfileRow, err error) {
	err = db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
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
	return db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
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
		if input.Identity != nil {
			input.Identity.UserID = input.SubjectID
			if _, err := q.UpdateMemberIdentity(ctx, *input.Identity); errors.Is(err, pgx.ErrNoRows) {
				return ErrProfileConflict
			} else if err != nil {
				return err
			}
			if _, err := q.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: input.ActorID, SubjectUserID: input.SubjectID, Action: "IDENTITY_UPDATED", ChangedFields: input.IdentityFields}); err != nil {
				return err
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

func (s PostgresProfileStore) SavePhoto(ctx context.Context, input ProfilePhotoUpdate) (oldKey *string, err error) {
	err = db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
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
		consentID, err := q.GetCurrentImageConsent(ctx, dbgen.GetCurrentImageConsentParams{UserID: input.SubjectID, DocumentVersion: input.ConsentVersion, DocumentSha256: input.ConsentSHA256})
		if errors.Is(err, pgx.ErrNoRows) {
			if input.IsAdmin || !input.AcceptConsent {
				return ErrConsentRequired
			}
			grantedBy := input.ActorID
			consent, createErr := q.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{UserID: input.SubjectID, GrantedByUserID: &grantedBy, ConsentType: "Uso_Imagem", DocumentVersion: input.ConsentVersion, DocumentSha256: input.ConsentSHA256, IpAddress: input.IP, UserAgent: input.UserAgent})
			if createErr != nil {
				return createErr
			}
			consentID = consent.ID
		} else if err != nil {
			return err
		}
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
	err = db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
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
	return dbgen.New(s.Pool).GetMemberAvatar(ctx, params)
}

func canViewProfile(profile dbgen.GetMemberProfileRow, actorID uuid.UUID, isAdmin bool) bool {
	return isAdmin || (profile.IsActive && (profile.ID == actorID || (profile.GuardianID != nil && *profile.GuardianID == actorID)))
}

func canEditProfile(profile dbgen.GetMemberProfileRow, actorID uuid.UUID, isAdmin bool) bool {
	return isAdmin || (profile.IsActive && ((!profile.IsDependent && profile.ID == actorID) || (profile.GuardianID != nil && *profile.GuardianID == actorID)))
}
