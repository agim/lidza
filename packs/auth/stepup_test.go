package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/credentials"
	qrcode "github.com/skip2/go-qrcode"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func authCall(t *testing.T, client *http.Client, method, url string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var input io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		input = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, url, input)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestTwoFactorStepUp(t *testing.T) {
	t.Setenv(credentials.EnvMasterKey, strings.Repeat("ab", 32))
	a := testAuth(t)
	var events []Event
	srv, client := signinServer(t, a, Options{Title: "Example", OnEvent: func(_ context.Context, e Event) { events = append(events, e) }})
	api := srv.URL + Prefix

	code, signed := call(t, client, http.MethodPost, api+"/register", map[string]any{"email": "factor@example.com", "password": "correct horse battery"})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %v", code, signed)
	}
	u, err := a.Verify(signed["accessToken"].(string))
	if err != nil {
		t.Fatal(err)
	}
	code, setup := call(t, client, http.MethodPost, api+"/two-factor/setup", map[string]any{})
	if code != http.StatusOK || setup["secret"] == "" || !strings.HasPrefix(setup["otpauthUrl"].(string), "otpauth://totp/Example:") || !strings.HasPrefix(setup["qrCodeDataUrl"].(string), "data:image/png;base64,") {
		t.Fatalf("setup: %d %v", code, setup)
	}
	encoded := strings.TrimPrefix(setup["qrCodeDataUrl"].(string), "data:image/png;base64,")
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("QR image: %v", err)
	}
	wantImage, err := qrcode.Encode(setup["otpauthUrl"].(string), qrcode.Medium, 256)
	if err != nil || !bytes.Equal(image, wantImage) {
		t.Fatal("QR image does not encode the returned otpauth URL")
	}
	secret := setup["secret"].(string)
	now := time.Now()
	current, _ := totpCode(secret, totpStep(now))
	code, backup := call(t, client, http.MethodPost, api+"/two-factor/enable", map[string]string{"code": current})
	if code != http.StatusOK || len(backup["codes"].([]any)) != backupCodeCount {
		t.Fatalf("enable: %d %v", code, backup)
	}
	code, status := call(t, client, http.MethodGet, api+"/two-factor", nil)
	if code != http.StatusOK || status["enabled"] != true || status["backupCodesLeft"] != float64(backupCodeCount) {
		t.Fatalf("status: %d %v", code, status)
	}

	services := lidza.NewServices()
	lidza.Provide(services, a)
	ctx := lidza.WithServices(context.Background(), services)
	ctx = context.WithValue(ctx, userKey{}, u)
	if err := a.CheckStepUp(ctx, "credential.reveal", "app/SECRET_A"); !errorsIs(err, ErrStepUpRequired) {
		t.Fatalf("ungranted step-up: %v", err)
	}
	recovery := backup["codes"].([]any)[0].(string)
	code, grant := call(t, client, http.MethodPost, api+"/step-up", map[string]string{"code": recovery, "purpose": "credential.reveal", "target": "app/SECRET_A"})
	if code != http.StatusOK || grant["expiresAt"] == nil {
		t.Fatalf("grant: %d %v", code, grant)
	}
	if err := a.CheckStepUp(ctx, "credential.reveal", "app/SECRET_A"); err != nil {
		t.Fatalf("exact grant refused: %v", err)
	}
	if err := a.CheckStepUp(ctx, "credential.reveal", "app/SECRET_B"); !errorsIs(err, ErrStepUpRequired) {
		t.Fatalf("grant crossed targets: %v", err)
	}
	lateServices := lidza.NewServices()
	lidza.Provide(lateServices, a)
	lidza.Provide[lidza.Clock](lateServices, fixedClock{now: now.Add(StepUpWindow + time.Minute)})
	late := lidza.WithServices(context.Background(), lateServices)
	late = context.WithValue(late, userKey{}, u)
	if err := a.CheckStepUp(late, "credential.reveal", "app/SECRET_A"); !errorsIs(err, ErrStepUpRequired) {
		t.Fatalf("expired grant: %v", err)
	}

	if err := AuthorizeStepUp(ctx, nil, "credential.reveal", "app/SECRET_A"); err != nil {
		t.Fatal(err)
	}
	if got := events[len(events)-1]; got.Kind != EventStepUpAuthorized || got.Target != "app/SECRET_A" {
		t.Fatalf("authorization event: %+v", got)
	}
	if err := AuthorizeStepUp(ctx, nil, "credential.reveal", "app/SECRET_B"); !errorsIs(err, ErrStepUpRequired) {
		t.Fatalf("rejected authorization: %v", err)
	}
	if got := events[len(events)-1]; got.Kind != EventStepUpRejected || got.Target != "app/SECRET_B" {
		t.Fatalf("rejection event: %+v", got)
	}

	if code, _ := call(t, client, http.MethodPost, api+"/logout", map[string]any{}); code != http.StatusNoContent {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := call(t, client, http.MethodPost, api+"/login", map[string]string{"email": "factor@example.com", "password": "correct horse battery"}); code != http.StatusForbidden {
		t.Fatalf("password login without factor: %d", code)
	}
	next, _ := totpCode(secret, totpStep(now)+1)
	if code, out := authCall(t, client, http.MethodPost, api+"/login", map[string]string{"email": "factor@example.com", "password": "correct horse battery"}, map[string]string{OTPHeader: next}); code != http.StatusOK || out["accessToken"] == nil {
		t.Fatalf("password login with factor: %d %v", code, out)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
