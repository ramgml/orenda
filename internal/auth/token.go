package auth

// TokenRow is the storage-layer projection of api_tokens used by auth
// middleware (and by the repo that backs it).
//
// It lives in the auth package so both internal/api (which only needs to
// reference the fields) and internal/storage/sqlite (which reads/writes the
// table) can depend on it without an import cycle.
type TokenRow struct {
	ID         string
	UserID     string
	Name       string
	Hash       string
	ScopesJSON string
}
