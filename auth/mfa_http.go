package auth

import (
	"errors"
	"net/http"
	"strings"

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

type mfaCompletionRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

// NewTOTPEnrollmentHandler starts TOTP enrollment for the authenticated
// principal. The issuer is server-controlled; the subject UUID is used as the
// default authenticator label so the handler never trusts a client-supplied
// account identity.
func NewTOTPEnrollmentHandler(service *MFAService, issuer string) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("MFA service is required")
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
			Secret:          enrollment.Secret,
			ProvisioningURI: enrollment.ProvisioningURI,
			ExpiresAt:       enrollment.ExpiresAt,
		})
	}), nil
}

// NewTOTPConfirmationHandler activates the pending TOTP factor for the
// authenticated principal. Confirmation invalidates all existing sessions,
// including the session used for enrollment, so the next login exercises MFA.
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
		if err := service.ConfirmTOTPEnrollment(r.Context(), principal.SubjectID(), request.Code); err != nil {
			web.Error(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
