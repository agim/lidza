package schema

// UserModels are the model names Owner treats as the app's users: an
// app's own User and the auth pack's AuthUser (auth.Mount's accounts).
var UserModels = []string{"User", "AuthUser"}

// Column is the field's column name in Postgres, unquoted: ownerId is
// owner_id.
func (f *Field) Column() string { return snake(f.Name) }

// Owner returns the field that ties a row of m to one user, or nil when
// m is not owned. The first match wins:
//
//  1. a field named ownerId;
//  2. a field named userId;
//  3. the first field with @ref(User) or @ref(AuthUser), such as
//     authorId uuid @ref(User).
//
// The id and arrays are never the owner. A model marked @public, and a
// type, have none. The owner's value is the signed-in user's id,
// auth.CurrentUser(ctx).ID.
func (s *Schema) Owner(m *Model) *Field {
	if m == nil || !m.Persisted || m.Public {
		return nil
	}
	candidate := func(f *Field) bool { return !f.ID && !f.Array }
	for _, name := range []string{"ownerId", "userId"} {
		for _, f := range m.Fields {
			if f.Name == name && candidate(f) {
				return f
			}
		}
	}
	for _, f := range m.Fields {
		if !candidate(f) {
			continue
		}
		for _, u := range UserModels {
			if f.Ref == u {
				return f
			}
		}
	}
	return nil
}
