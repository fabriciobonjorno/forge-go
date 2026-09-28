package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fabriciobonjorno/forge-go/web"
)

type totpEnrollmentResponse struct {
	Secret          string    `json:"secret"`
	ProvisioningURI string    `json:"provisioning_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type totpConfirmationRequest struct {
	Code string `json:"code"`
}

type totpConfirmationResponse struct {
	BackupCodes []string `json:"backup_codes"`
}

type mfaCompletionRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

type MFAEnrollmentAuthorizer interface {
	AuthorizeMFAEnrollment(ctx context.Context, principal Principal) error
}

type MFAEnrollmentAuthorizerFunc func(context.Context, Principal) error

func (fn MFAEnrollmentAuthorizerFunc) AuthorizeMFAEnrollment(ctx context.Context, principal Principal) error {
	return fn(ctx, principal)
}

// NewTOTPEnrollmentHandler starts TOTP enrollment for the authenticated
// principal only after an application-supplied step-up authorizer succeeds.
// The issuer is server-controlled; the subject UUID is used as the default
// authenticator label so the handler never trusts a client-supplied identity.
func NewTOTPEnrollmentHandler(service *MFAService, issuer string, authorizer MFAEnrollmentAuthorizer) (http.Handler, error) {
	if service == nil || authorizer == nil {
		return nil, errors.New("MFA service and enrollment authorizer are required")
	}
	issuer = strings.TrimSpace(issuer)
	if !validTOTPLabel(issuer, 128) {
		return nil, errors.New("TOTP issuer is required and must not contain control characters")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		principal, ok := FromContext(r.Context())
		if !ok {
			web.Error(w, r, ErrCredentialsRequired)
			return
		}
		if err := authorizer.AuthorizeMFAEnrollment(r.Context(), principal); err != nil {
			web.Error(w, r, err)
			return
		}
		enrollment, err := service.BeginTOTPEnrollment(
			r.Context(),
			principal.SubjectID(),
			issuer,
			principal.SubjectID().String(),
		)
		if err != nil {
			web.Error(w, r, err)
			return
		}
		web.JSON(w, http.StatusOK, totpEnrollmentResponse{
			Secret:          enrollment.Secret.Reveal(),
			ProvisioningURI: enrollment.ProvisioningURI.Reveal(),
			ExpiresAt:       enrollment.ExpiresAt,
		})
	}), nil
}

// NewTOTPConfirmationHandler activates the pending TOTP factor for the
// authenticated principal. Confirmation invalidates all existing sessions,
// including the session used for enrollment, and returns the one-time backup
// codes exactly once so the next login exercises MFA.
func NewTOTPConfirmationHandler(service *MFAService) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("MFA service is required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		principal, ok := FromContext(r.Context())
		if !ok {
			web.Error(w, r, ErrCredentialsRequired)
			return
		}
		var request totpConfirmationRequest
		if err := web.DecodeJSON(r, &request); err != nil {
			web.Error(w, r, err)
			return
		}
		confirmation, err := service.ConfirmTOTPEnrollment(r.Context(), principal.SubjectID(), request.Code)
		if err != nil {
			web.Error(w, r, err)
			return
		}
		backupCodes := make([]string, len(confirmation.BackupCodes))
		for index, backupCode := range confirmation.BackupCodes {
			backupCodes[index] = backupCode.Reveal()
		}
		web.JSON(w, http.StatusOK, totpConfirmationResponse{BackupCodes: backupCodes})
	}), nil
}

// NewMFACompletionHandler exchanges a valid password-login challenge plus TOTP
// code for the normal opaque bearer session.
func NewMFACompletionHandler(service *MFAService) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("MFA service is required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		var request mfaCompletionRequest
		if err := web.DecodeJSON(r, &request); err != nil {
			web.Error(w, r, err)
			return
		}
		result, err := service.CompleteLogin(r.Context(), MFACompletion{
			ChallengeToken: request.ChallengeToken,
			Code:           request.Code,
			Source:         requestSource(r),
		})
		if err != nil {
			var throttled *LoginThrottledError
			if errors.As(err, &throttled) {
				w.Header().Set("Retry-After", retryAfterHeader(throttled.RetryAfter))
			}
			web.Error(w, r, err)
			return
		}
		web.JSON(w, http.StatusOK, loginResponse{
			AccessToken: result.Token.Reveal(),
			TokenType:   "Bearer",
			ExpiresAt:   result.ExpiresAt,
		})
	}), nil
}


type MFAChangeAuthorizer interface {
	AuthorizeMFAChange(ctx context.Context, principal Principal) error
}

type MFAChangeAuthorizerFunc func(context.Context, Principal) error

func (fn MFAChangeAuthorizerFunc) AuthorizeMFAChange(ctx context.Context, principal Principal) error {
	return fn(ctx, principal)
}

// NewTOTPRotationHandler starts replacement of an active TOTP factor. The
// current factor remains active until the new seed is confirmed.
func NewTOTPRotationHandler(service *MFAService, issuer string, authorizer MFAChangeAuthorizer) (http.Handler, error) {
	if service == nil || authorizer == nil {
		return nil, errors.New("MFA service and change authorizer are required")
	}
	issuer = strings.TrimSpace(issuer)
	if !validTOTPLabel(issuer, 128) {
		return nil, errors.New("TOTP issuer is required and must not contain control characters")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		principal, ok := FromContext(r.Context())
		if !ok {
			web.Error(w, r, ErrCredentialsRequired)
			return
		}
		if err := authorizer.AuthorizeMFAChange(r.Context(), principal); err != nil {
			web.Error(w, r, err)
			return
		}
		enrollment, err := service.BeginTOTPRotation(
			r.Context(),
			principal.SubjectID(),
			issuer,
			principal.SubjectID().String(),
		)
		if err != nil {
			web.Error(w, r, err)
			return
		}
		web.JSON(w, http.StatusOK, totpEnrollmentResponse{
			Secret:          enrollment.Secret.Reveal(),
			ProvisioningURI: enrollment.ProvisioningURI.Reveal(),
			ExpiresAt:       enrollment.ExpiresAt,
		})
	}), nil
}

// NewTOTPRotationConfirmationHandler activates the pending replacement factor,
// creates a fresh backup-code set, and revokes all sessions.
func NewTOTPRotationConfirmationHandler(service *MFAService, authorizer MFAChangeAuthorizer) (http.Handler, error) {
	if service == nil || authorizer == nil {
		return nil, errors.New("MFA service and change authorizer are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		principal, ok := FromContext(r.Context())
		if !ok {
			web.Error(w, r, ErrCredentialsRequired)
			return
		}
		if err := authorizer.AuthorizeMFAChange(r.Context(), principal); err != nil {
			web.Error(w, r, err)
			return
		}
		var request totpConfirmationRequest
		if err := web.DecodeJSON(r, &request); err != nil {
			web.Error(w, r, err)
			return
		}
		confirmation, err := service.ConfirmTOTPRotation(r.Context(), principal.SubjectID(), request.Code)
		if err != nil {
			web.Error(w, r, err)
			return
		}
		backupCodes := make([]string, len(confirmation.BackupCodes))
		for index, backupCode := range confirmation.BackupCodes {
			backupCodes[index] = backupCode.Reveal()
		}
		web.JSON(w, http.StatusOK, totpConfirmationResponse{BackupCodes: backupCodes})
	}), nil
}

// NewMFADisableHandler disables the active factor only after application-
// supplied step-up authorization. Disabling MFA revokes all current sessions.
func NewMFADisableHandler(service *MFAService, authorizer MFAChangeAuthorizer) (http.Handler, error) {
	if service == nil || authorizer == nil {
		return nil, errors.New("MFA service and change authorizer are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		principal, ok := FromContext(r.Context())
		if !ok {
			web.Error(w, r, ErrCredentialsRequired)
			return
		}
		if err := authorizer.AuthorizeMFAChange(r.Context(), principal); err != nil {
			web.Error(w, r, err)
			return
		}
		if err := service.DisableMFA(r.Context(), principal.SubjectID()); err != nil {
			web.Error(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), nil
}
