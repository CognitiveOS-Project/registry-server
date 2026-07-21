package auth

import "context"

type contextKey string

const (
	FingerprintKey contextKey = "fingerprint"
	OwnerKeyRef    contextKey = "owner_key"
)

func ContextWithFingerprint(ctx context.Context, fp string) context.Context {
	return context.WithValue(ctx, FingerprintKey, fp)
}

func FingerprintFromContext(ctx context.Context) (string, bool) {
	fp, ok := ctx.Value(FingerprintKey).(string)
	return fp, ok
}

func ContextWithOwnerKey(ctx context.Context, owner *Owner, key *OwnerKey) context.Context {
	ctx = context.WithValue(ctx, OwnerKeyRef, &ownerKeyRef{owner, key})
	return ContextWithFingerprint(ctx, key.Fingerprint)
}

func OwnerKeyFromContext(ctx context.Context) (*Owner, *OwnerKey, bool) {
	ref, ok := ctx.Value(OwnerKeyRef).(*ownerKeyRef)
	if !ok {
		return nil, nil, false
	}
	return ref.owner, ref.key, true
}

type ownerKeyRef struct {
	owner *Owner
	key   *OwnerKey
}
