package social

import (
	"fmt"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// inputFieldError retains the governed length failure for REST's field-key
// response while remaining ErrInvalid to service callers and other adapters.
type inputFieldError struct {
	field string
	limit int
}

func (e *inputFieldError) Error() string { return platform.ErrInvalid.Error() }
func (e *inputFieldError) Unwrap() error { return platform.ErrInvalid }

// Presence checks activity visibility, current membership and its time window
// before returning this value failure. The REST transit adapter maps it to the
// source action's forbidden response without changing other invalid requests.
var errInvalidTransitStatus = fmt.Errorf("%w: invalid transit status", platform.ErrInvalid)
