package panel

import "testing"

func TestOAuthIdentity(t *testing.T) {
	got, err := oauthIdentity("google", []byte(`{"sub":"abc","name":"Ada Lovelace"}`))
	if err != nil || got.UID != "abc" || got.Name != "Ada Lovelace" {
		t.Fatalf("google: %+v %v", got, err)
	}
	got, err = oauthIdentity("github", []byte(`{"id":42,"name":"","login":"octocat"}`))
	if err != nil || got.UID != "42" || got.Name != "octocat" {
		t.Fatalf("github login: %+v %v", got, err)
	}
	got, err = oauthIdentity("yandex", []byte(`{"id":"ya-9","real_name":"Ivan Ivanov","display_name":"ivan"}`))
	if err != nil || got.UID != "ya-9" || got.Name != "Ivan Ivanov" {
		t.Fatalf("yandex: %+v %v", got, err)
	}
	if _, err := oauthIdentity("google", []byte(`{}`)); err == nil {
		t.Fatal("empty sub must be an error")
	}
}
