package domainverify

import (
	"errors"
	"time"
)

// MaxExtends is the upper bound on TTL extensions per spec § 7 (hard rule § 15 #7).
const MaxExtends = 2

// ExtendTickDuration is the amount added per extend (spec § 7.2).
const ExtendTickDuration = 24 * time.Hour

// ErrCannotExtend is returned when extend is not permitted: already verified,
// already expired, or no extensions remain.
var ErrCannotExtend = errors.New("cannot extend")

// ExtendInput captures the binding fields needed to decide an extend.
type ExtendInput struct {
	Now         time.Time
	Verified    bool
	ExtendsUsed int
	ExpiresAt   time.Time
}

// ExtendResult carries the new expiry & counter.
type ExtendResult struct {
	NewExpiresAt   time.Time
	NewExtendsUsed int
}

// ApplyExtend computes the new expiry & extends_used or returns ErrCannotExtend.
// Hard rule § 15 #7: never permit a third extend (max 2 × 24h = 72h total).
func ApplyExtend(in ExtendInput) (ExtendResult, error) {
	if in.Verified {
		return ExtendResult{}, errors.Join(ErrCannotExtend, errors.New("already verified"))
	}
	if !in.ExpiresAt.After(in.Now) {
		return ExtendResult{}, errors.Join(ErrCannotExtend, errors.New("already expired"))
	}
	if in.ExtendsUsed >= MaxExtends {
		return ExtendResult{}, errors.Join(ErrCannotExtend, errors.New("max extends reached"))
	}
	return ExtendResult{
		NewExpiresAt:   in.ExpiresAt.Add(ExtendTickDuration),
		NewExtendsUsed: in.ExtendsUsed + 1,
	}, nil
}
