package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func Login(email, password string) (string, string, error) {
	body, err := json.Marshal(map[string]any{"email": email, "password": password, "offset_time": 120})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.tapas.io/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("X-Offset-Time", "120")
	req.Header.Set("Accept", "application/panda+json")
	req.Header.Set("X-Device-Type", "ANDROID")
	req.Header.Set("X-Device-Uuid", hex.EncodeToString([]byte("kahuwolp")))
	req.Header.Set("X-Lang-Code", "en")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := (&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || next.URL.Scheme != "https" || next.URL.Hostname() != "api.tapas.io" || next.URL.Port() != "" || next.URL.User != nil {
			return errors.New("Tapas login redirected to an unsupported host")
		}
		return nil
	}}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized {
		return "", "", errors.New("email or password invalid")
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("login: %s", res.Status)
	}
	userID, token := res.Header.Get("X-User-Id"), res.Header.Get("X-Auth-Token")
	if userID == "" || token == "" {
		return "", "", errors.New("login response did not contain a user ID and token")
	}
	return userID, token, nil
}

func BuildHeader(userID, token string) (http.Header, error) {
	if userID == "" || token == "" {
		return nil, errors.New("user ID or auth token is missing")
	}
	head := http.Header{}
	head.Set("Accept", "application/panda+json")
	head.Set("Content-Type", "application/json;charset=utf-8")
	head.Set("X-Device-Type", "ANDROID")
	head.Set("X-Device-Uuid", hex.EncodeToString([]byte("kahuwolp")))
	head.Set("X-Lang-Code", "en")
	head.Set("User-Agent", "okhttp 31812; OS Version 16; phone; App Version ZERO")
	head.Set("X-User-Id", userID)
	head.Set("X-Auth-Token", token)
	return head, nil
}
