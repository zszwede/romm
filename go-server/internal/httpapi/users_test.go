package httpapi

import (
	"testing"

	"github.com/zszwede/romm-go/internal/store"
)

func TestUserCRUD_Endpoints(t *testing.T) {
	fs := &fakeStore{userID: 1, authUser: &store.UserRecord{ID: 1, Username: "xiam", Role: "admin", Enabled: true}}
	s := New(Deps{Store: fs, AuthSecret: testSecret})
	admin := scopedToken(t, "users.read users.write me.read")

	// Create.
	w := do(s, "POST", "/api/users", admin, `{"username":"bob","password":"pw","email":"b@x.com","role":"user"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	if m["username"] != "bob" || m["role"] != "user" {
		t.Fatalf("created user: %v", m)
	}
	// Missing creds -> 400.
	if w := do(s, "POST", "/api/users", admin, `{"username":"x"}`); w.Code != 400 {
		t.Errorf("no password: %d want 400", w.Code)
	}
	// Non-admin scope -> 401.
	readonly := scopedToken(t, "me.read")
	if w := do(s, "POST", "/api/users", readonly, `{"username":"a","password":"b"}`); w.Code != 401 {
		t.Errorf("non-admin create: %d want 401", w.Code)
	}
	// Delete.
	if w := do(s, "DELETE", "/api/users/2", admin, ""); w.Code != 204 {
		t.Errorf("delete: %d want 204", w.Code)
	}
	if w := do(s, "DELETE", "/api/users/999", admin, ""); w.Code != 404 {
		t.Errorf("delete missing: %d want 404", w.Code)
	}
	// Invite link.
	w = do(s, "POST", "/api/users/invite-link?role=user", admin, "")
	if w.Code != 201 || decodeBody(t, w.Body.Bytes())["token"] == nil {
		t.Errorf("invite link: %d %s", w.Code, w.Body.String())
	}
}
