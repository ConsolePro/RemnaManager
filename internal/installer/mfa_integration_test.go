package installer

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Exercise the real portal's password challenge, first-factor-only denial,
// TOTP enrollment/login and API-key creation. Nothing here accesses a real user.
func exerciseMFAPortal(t *testing.T, client *http.Client, s State, enrolledSecret string) string {
	t.Helper()
	base := "https://" + s.Params.PanelDomain
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	request := func(method, target string, form url.Values) (*http.Response, string) {
		t.Helper()
		if strings.HasPrefix(target, "/") {
			target = base + target
		}
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host != s.Params.PanelDomain {
			t.Fatal("portal redirected outside its HTTPS origin")
		}
		body := ""
		if form != nil {
			body = form.Encode()
		}
		req, _ := http.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("User-Agent", "Mozilla/5.0 Gecko/20100101 Firefox/140.0")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return resp, string(data)
	}
	resp, _ := request("GET", "/r/login", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("MFA login page: HTTP %d", resp.StatusCode)
	}
	resp, _ = request("POST", "/r/login", url.Values{"username": {s.Params.Admin}, "realm": {"local"}})
	if resp.StatusCode != 303 {
		t.Fatalf("MFA identity challenge: HTTP %d", resp.StatusCode)
	}
	sandbox := resp.Header.Get("Location")
	if !strings.Contains(sandbox, "/sandbox/") {
		t.Fatal("missing MFA sandbox")
	}
	resp, _ = request("POST", sandbox+"/auth", url.Values{"secret": {"wrong-password"}})
	if resp.StatusCode < 400 {
		t.Fatal("incorrect MFA portal password accepted")
	}
	resp, _ = request("POST", sandbox+"/auth", url.Values{"secret": {s.MFAPassword}})
	if resp.StatusCode != 303 {
		t.Fatalf("MFA password challenge: HTTP %d", resp.StatusCode)
	}
	resp, page := request("GET", sandbox, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("MFA factor selection: HTTP %d", resp.StatusCode)
	}
	resp, _ = request("GET", "/auth/login", nil)
	if resp.StatusCode == 200 {
		t.Fatal("password alone bypassed required MFA")
	}
	challenge := sandbox + "/mfa-app-auth"
	form := url.Values{}
	if enrolledSecret == "" {
		challenge = sandbox + "/mfa-app-register"
		_, page = request("GET", challenge, nil)
		for _, input := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(page, -1) {
			name := regexp.MustCompile(`\bname="([^"]+)"`).FindStringSubmatch(input)
			value := regexp.MustCompile(`\bvalue="([^"]*)"`).FindStringSubmatch(input)
			if len(name) == 2 && len(value) == 2 {
				form.Set(name[1], html.UnescapeString(value[1]))
			}
		}
		enrolledSecret = form.Get("secret")
		if enrolledSecret == "" {
			t.Fatal("MFA enrollment did not offer a TOTP secret")
		}
		form.Set("period", "30")
		form.Set("digits", "6")
		form.Set("type", "totp")
	}
	form.Set("passcode", "not-a-code")
	resp, _ = request("POST", challenge, form)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		t.Fatal("invalid second factor accepted")
	}
	resp, _ = request("GET", "/auth/login", nil)
	if resp.StatusCode == 200 {
		t.Fatal("invalid second factor unlocked the panel")
	}
	form.Set("passcode", fixtureTOTP(enrolledSecret, time.Now()))
	resp, _ = request("POST", challenge, form)
	if resp.StatusCode != 303 {
		t.Fatalf("valid TOTP challenge: HTTP %d", resp.StatusCode)
	}
	resp, _ = request("GET", sandbox, nil)
	for i := 0; i < 6 && resp.StatusCode >= 300 && resp.StatusCode < 400; i++ {
		resp, _ = request("GET", resp.Header.Get("Location"), nil)
	}
	resp, page = request("GET", "/auth/login", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "fixture backend reached") {
		t.Fatalf("full MFA login did not reach panel: HTTP %d", resp.StatusCode)
	}
	resp, page = request("POST", "/r/settings/apikeys/add", url.Values{"comment1": {"RemnaManager disposable test"}})
	keyMatch := regexp.MustCompile(`<code[^>]*>([^<]+)</code>`).FindStringSubmatch(page)
	if resp.StatusCode != 200 || len(keyMatch) != 2 {
		t.Fatalf("MFA portal API key creation: HTTP %d", resp.StatusCode)
	}
	apiKey := strings.TrimSpace(html.UnescapeString(keyMatch[1]))
	client.Jar = nil
	for _, gate := range []bool{false, true} {
		for _, bearer := range []bool{false, true} {
			req, _ := http.NewRequest("GET", base+"/api/nodes", nil)
			key := "incorrect-key"
			if gate {
				key = apiKey
			}
			req.Header.Set("X-Api-Key", key)
			value := "incorrect"
			if bearer {
				value = "fixture-panel-token"
			}
			req.Header.Set("Authorization", "Bearer "+value)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if (resp.StatusCode == 200) != (gate && bearer) {
				t.Fatalf("MFA API must require both keys: gate=%v bearer=%v HTTP=%d", gate, bearer, resp.StatusCode)
			}
		}
	}
	client.Jar = jar
	request("GET", "/r/logout", nil)
	resp, _ = request("GET", "/auth/login", nil)
	if resp.StatusCode == 200 {
		t.Fatal("logout left an authenticated browser")
	}
	client.Jar = nil
	return enrolledSecret
}

func fixtureTOTP(secret string, at time.Time) string {
	mac := hmac.New(sha1.New, []byte(secret))
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(at.Unix()/30))
	mac.Write(counter)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}
