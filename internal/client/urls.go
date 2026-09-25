package client

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func AllowedImageURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "tapas.io" || strings.HasSuffix(host, ".tapas.io") || host == "d30womf5coomej.cloudfront.net"
}

func CheckImageRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || !AllowedImageURL(req.URL) {
		return errors.New("image redirected to an unsupported host")
	}
	return nil
}
