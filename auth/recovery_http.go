package auth

import (
	"errors"
	"net/http"

	"github.com/fabriciobonjorno/forge-go/web"
)

type recoveryRequestBody struct {
	Email      string `json:"email"`
	TenantSlug string `json:"tenant"`
}

type recoveryAcceptedResponse struct {
	Status string `json:"status"`
}

type passwordResetBody struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func NewPasswordRecoveryRequestHandler(service *RecoveryService) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("recovery service is required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request recoveryRequestBody
		if err := web.DecodeJSON(r, &request); err != nil {
			web.Error(w, r, err)
			return
		}
		err := service.Request(r.Context(), RecoveryRequest{
			Email:      request.Email,
			TenantSlug: request.TenantSlug,
			Source:     requestSource(r),
		})
		if err != nil {
			var throttled *RecoveryThrottledError
			if errors.As(err, &throttled) {
				w.Header().Set("Retry-After", retryAfterHeader(throttled.RetryAfter))
			}
			web.Error(w, r, err)
			return
		}
		recoveryNoStore(w)
		web.JSON(w, http.StatusAccepted, recoveryAcceptedResponse{Status: "accepted"})
	}), nil
}

func NewPasswordResetHandler(service *RecoveryService) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("recovery service is required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request passwordResetBody
		if err := web.DecodeJSON(r, &request); err != nil {
			web.Error(w, r, err)
			return
		}
		if err := service.Reset(r.Context(), PasswordReset{
			Token:       request.Token,
			NewPassword: request.NewPassword,
		}); err != nil {
			web.Error(w, r, err)
			return
		}
		recoveryNoStore(w)
		w.WriteHeader(http.StatusNoContent)
	}), nil
}

func recoveryNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
