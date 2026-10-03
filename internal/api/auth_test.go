package api

import (
	"encoding/hex"
	"testing"
)

func TestBuildHeaderPreservesOriginalValues(t *testing.T) {
	t.Setenv("TAPAS_LANG_CODE", "de")
	header, err := BuildHeader("user", "token")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Accept":        "application/panda+json",
		"Content-Type":  "application/json;charset=utf-8",
		"X-Device-Type": "ANDROID",
		"X-Device-Uuid": hex.EncodeToString([]byte("kahuwolp")),
		"X-Lang-Code":   "en",
		"User-Agent":    "okhttp 31812; OS Version 16; phone; App Version 23",
		"X-User-Id":     "user",
		"X-Auth-Token":  "token",
	}
	for key, value := range want {
		if got := header.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}
