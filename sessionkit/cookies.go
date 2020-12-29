package sessionkit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

var SecretBytes []byte
var CookieDomain string

func checkCookieSetup() {
	if len(SecretBytes) == 0 {
		panic(errors.New("sessionkit.SecretBytes not configured"))
	}
	if len(CookieDomain) == 0 {
		panic(errors.New("sessionkit.CooikeDomain not configured"))
	}
}

func setCookieValue(c *web.Context, cookieName string, value []byte, permanentCookie bool) {
	checkCookieSetup()
	userTimestamp := fmt.Sprintf("%d", time.Now().UTC().Unix())
	mac := hmac.New(sha256.New, SecretBytes)
	mac.Write(value)
	mac.Write([]byte(userTimestamp))
	var expires time.Time
	if permanentCookie {
		expires = time.Now().Add(300 * 24 * time.Hour)
	}
	http.SetCookie(c, &http.Cookie{
		HttpOnly: true,
		Name:     cookieName,
		Path:     "/",
		Expires:  expires,
		Value:    fmt.Sprintf("%s.%s.%s", hex.EncodeToString(value), userTimestamp, hex.EncodeToString(mac.Sum(nil))),
		Domain:   CookieDomain,
	})
	c.SetData("cookie."+cookieName, value)
}

func deleteCookieValue(c *web.Context, cookieName string) {
	c.RemoveData("cookie." + cookieName)

	http.SetCookie(c, &http.Cookie{
		Name:    cookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		Domain:  CookieDomain,
	})
}

func getCookieValue(c *web.Context, cookieName string) []byte {
	checkCookieSetup()

	// check if user already exists in the web context
	if v, ok := c.GetData("cookie." + cookieName); ok {
		return v.([]byte)
	}

	// if there already is a set-cookie user, let's use that!
	if cookies, ok := c.Header()["Set-Cookie"]; ok {
		for _, line := range cookies {
			parts := strings.Split(strings.TrimSpace(line), "=")
			if parts[0] == cookieName {
				if buf, _, ok := parseCookieValue(parts[1][:strings.Index(parts[1], ";")]); ok {
					return buf
				}
			}
		}
	}

	// check the cookie by name. Note the loop used to check cookies with the
	// same name (but different domain).
	for _, cookie := range c.Request.Cookies() {
		if cookie.Name != cookieName {
			continue
		}

		if buf, ti, ok := parseCookieValue(cookie.Value); ok {
			// refresh cookie, if it's older than an hour.
			now := time.Now()
			if cookie.Expires.After(now) {
				if ti.Before(time.Now().UTC().Add(-time.Hour)) {
					setCookieValue(c, cookieName, buf, true)
				}
			}

			return buf
		}
	}

	return nil
}

func parseCookieValue(cookie string) ([]byte, time.Time, bool) {
	parts := strings.Split(cookie, ".")
	if len(parts) == 3 {
		cookieIDString := parts[0]
		cookieTimestamp := parts[1]
		cookieAuth := parts[2]
		dehexed, err := hex.DecodeString(cookieIDString)
		if err != nil {
			return nil, time.Now(), false
		}

		mac := hmac.New(sha256.New, SecretBytes)
		mac.Write(dehexed)
		mac.Write([]byte(cookieTimestamp))

		if data, err := hex.DecodeString(cookieAuth); err == nil && hmac.Equal(data, mac.Sum(nil)) {
			seconds, err := strconv.ParseInt(cookieTimestamp, 10, 64)
			if err == nil && seconds > 0 && (time.Now().UTC().Unix()-seconds) < 60*60*24*60 {
				return dehexed, time.Unix(seconds, 0), true
			}
		}
	}

	return nil, time.Now(), false
}
