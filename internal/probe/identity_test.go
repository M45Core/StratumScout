package probe

import "testing"

func TestRandomIdentity(t *testing.T) {
	a, err := RandomIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Username) < 30 || a.Agent == "" || len(a.PayoutScript) != 25 {
		t.Fatalf("bad identity: %+v", a)
	}
	b, err := RandomIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if a.Username == b.Username {
		t.Fatal("identities should rotate")
	}
}
