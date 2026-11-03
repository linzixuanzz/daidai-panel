package handler_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// issue #175 通行密钥。这里用测试自建的 ECDSA P-256 密钥按 WebAuthn 格式拼 authenticatorData / clientDataJSON / 签名
// （注册用 none attestation，attestationObject 用 CBOR 编码），走真实的 HTTP 接口与 go-webauthn 的校验。

const (
	passkeyTestOrigin = "https://panel.example.com"
	passkeyTestRPID   = "panel.example.com"

	authFlagUP = 0x01
	authFlagUV = 0x04
	authFlagBE = 0x08
	authFlagBS = 0x10
	authFlagAT = 0x40
)

var b64url = base64.RawURLEncoding

// testAuthenticator 是测试里的「认证器」：一把 P-256 私钥 + 凭据 id + 注册时拿到的用户句柄。
type testAuthenticator struct {
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	signCount  uint32
	extraFlags byte // 比如 BE|BS：模拟会同步的通行密钥
}

func newTestAuthenticator(t *testing.T) *testAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	credID := make([]byte, 16)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("credential id: %v", err)
	}
	return &testAuthenticator{key: key, credID: credID}
}

func (a *testAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	ecdhKey, err := a.key.PublicKey.ECDH()
	if err != nil {
		t.Fatalf("ecdh public key: %v", err)
	}
	raw := ecdhKey.Bytes() // 0x04 || X || Y
	encoded, err := webauthncbor.Marshal(map[int]interface{}{
		1:  2,  // kty: EC2
		3:  -7, // alg: ES256
		-1: 1,  // crv: P-256
		-2: raw[1:33],
		-3: raw[33:65],
	})
	if err != nil {
		t.Fatalf("encode cose key: %v", err)
	}
	return encoded
}

func passkeyAuthData(rpID string, flags byte, counter uint32, attested []byte) []byte {
	rpHash := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, rpHash[:]...)
	out = append(out, flags)
	counterBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(counterBytes, counter)
	out = append(out, counterBytes...)
	return append(out, attested...)
}

func passkeyClientData(t *testing.T, ceremonyType, challenge, origin string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"type":        ceremonyType,
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatalf("client data: %v", err)
	}
	return data
}

// register 按 register/begin 返回的 options 生成 navigator.credentials.create 的结果 JSON
func (a *testAuthenticator) register(t *testing.T, options map[string]interface{}, origin string) map[string]interface{} {
	t.Helper()
	rp, _ := options["rp"].(map[string]interface{})
	user, _ := options["user"].(map[string]interface{})
	handle, err := b64url.DecodeString(fmt.Sprint(user["id"]))
	if err != nil {
		t.Fatalf("decode user.id %v: %v", user["id"], err)
	}
	a.userHandle = handle

	attested := make([]byte, 16) // AAGUID 全零
	idLen := make([]byte, 2)
	binary.BigEndian.PutUint16(idLen, uint16(len(a.credID)))
	attested = append(attested, idLen...)
	attested = append(attested, a.credID...)
	attested = append(attested, a.coseKey(t)...)
	authData := passkeyAuthData(fmt.Sprint(rp["id"]), authFlagUP|authFlagUV|authFlagAT|a.extraFlags, 0, attested)

	attestationObject, err := webauthncbor.Marshal(map[string]interface{}{
		"fmt":      "none",
		"attStmt":  map[string]interface{}{},
		"authData": authData,
	})
	if err != nil {
		t.Fatalf("encode attestation object: %v", err)
	}
	clientData := passkeyClientData(t, "webauthn.create", fmt.Sprint(options["challenge"]), origin)
	return map[string]interface{}{
		"id":    b64url.EncodeToString(a.credID),
		"rawId": b64url.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]interface{}{
			"clientDataJSON":    b64url.EncodeToString(clientData),
			"attestationObject": b64url.EncodeToString(attestationObject),
			"transports":        []string{"internal"},
		},
	}
}

type assertionOptions struct {
	rpID       string // 默认取 options.rpId
	userHandle []byte // 默认取注册时拿到的句柄
	flags      byte   // 默认 UP|UV|extraFlags
	tamper     bool   // 签完再改一个字节
}

// assert 按 login/begin 返回的 options 生成 navigator.credentials.get 的结果 JSON
func (a *testAuthenticator) assert(t *testing.T, options map[string]interface{}, origin string, opts assertionOptions) map[string]interface{} {
	t.Helper()
	rpID := opts.rpID
	if rpID == "" {
		rpID = fmt.Sprint(options["rpId"])
	}
	handle := opts.userHandle
	if handle == nil {
		handle = a.userHandle
	}
	flags := opts.flags
	if flags == 0 {
		flags = authFlagUP | authFlagUV | a.extraFlags
	}
	a.signCount++
	authData := passkeyAuthData(rpID, flags, a.signCount, nil)
	clientData := passkeyClientData(t, "webauthn.get", fmt.Sprint(options["challenge"]), origin)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}
	if opts.tamper {
		signature[len(signature)-1] ^= 0xff
	}
	return map[string]interface{}{
		"id":    b64url.EncodeToString(a.credID),
		"rawId": b64url.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]interface{}{
			"clientDataJSON":    b64url.EncodeToString(clientData),
			"authenticatorData": b64url.EncodeToString(authData),
			"signature":         b64url.EncodeToString(signature),
			"userHandle":        b64url.EncodeToString(handle),
		},
	}
}

func passkeyRequest(engine *gin.Engine, method, path string, body interface{}, token, origin, ip string) *httptest.ResponseRecorder {
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if origin != "" {
		headers["Origin"] = origin
	}
	payload := ""
	switch v := body.(type) {
	case nil:
	case string:
		payload = v
	default:
		raw, _ := json.Marshal(v)
		payload = string(raw)
	}
	return performJSONRequest(engine, method, path, payload, headers, ip)
}

// beginPasskey 调 begin 并返回 session_id 与 options
func beginPasskey(t *testing.T, engine *gin.Engine, path string, body interface{}, token, origin, ip string) (string, map[string]interface{}) {
	t.Helper()
	rec := passkeyRequest(engine, http.MethodPost, path, body, token, origin, ip)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d body=%s", path, rec.Code, rec.Body.String())
	}
	payload := decodeJSONMap(t, rec)
	data, _ := payload["data"].(map[string]interface{})
	sessionID, _ := data["session_id"].(string)
	options, _ := data["options"].(map[string]interface{})
	if sessionID == "" || options == nil {
		t.Fatalf("%s: missing session_id / options: %s", path, rec.Body.String())
	}
	return sessionID, options
}

// mustRegisterPasskey 走完整的注册流程，返回接口回的那一条
func mustRegisterPasskey(t *testing.T, engine *gin.Engine, token, password, origin, name string, auth *testAuthenticator) map[string]interface{} {
	t.Helper()
	sessionID, options := beginPasskey(t, engine, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, token, origin, "198.51.100.200")
	rec := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/finish", map[string]interface{}{
		"session_id": sessionID,
		"name":       name,
		"credential": auth.register(t, options, origin),
	}, token, origin, "198.51.100.200")
	if rec.Code != http.StatusOK {
		t.Fatalf("register finish: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONMap(t, rec)
	if payload["message"] != "通行密钥已添加" {
		t.Fatalf("unexpected register message: %s", rec.Body.String())
	}
	data, _ := payload["data"].(map[string]interface{})
	return data
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lastLoginLog(t *testing.T) model.LoginLog {
	t.Helper()
	var log model.LoginLog
	if err := database.DB.Order("id DESC").First(&log).Error; err != nil {
		t.Fatalf("query login log: %v", err)
	}
	return log
}

func TestPasskeyLoginBeginDerivesRPFromOrigin(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	rejected := []string{
		"",
		"null",
		"http://192.168.1.2:5700",
		"http://panel.example.com",
		"https://10.0.0.1",
		"https://[::1]:8443",
		"ftp://panel.example.com",
		"https://panel.example.com/login",
	}
	for i, origin := range rejected {
		rec := passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/begin", `{}`, "", origin, fmt.Sprintf("198.51.100.%d", 10+i))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("origin %q: expected 400, got %d body=%s", origin, rec.Code, rec.Body.String())
		}
		payload := decodeJSONMap(t, rec)
		if payload["code"] != "passkey_unsupported_origin" || payload["error"] != "当前访问地址不支持通行密钥，请通过 HTTPS 域名访问面板" {
			t.Fatalf("origin %q: unexpected body %s", origin, rec.Body.String())
		}
	}

	accepted := map[string]string{
		"https://Panel.Example.com:8443": "panel.example.com",
		"https://panel.example.com":      "panel.example.com",
		"http://localhost:5700":          "localhost",
	}
	for origin, wantRPID := range accepted {
		_, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", origin, "198.51.100.40")
		if options["rpId"] != wantRPID {
			t.Fatalf("origin %q: rpId = %#v, want %q", origin, options["rpId"], wantRPID)
		}
		if options["userVerification"] != "required" {
			t.Fatalf("origin %q: userVerification = %#v", origin, options["userVerification"])
		}
		if _, exists := options["allowCredentials"]; exists {
			t.Fatalf("origin %q: discoverable login must not list allowCredentials", origin)
		}
		if challenge, _ := options["challenge"].(string); challenge == "" {
			t.Fatalf("origin %q: missing challenge", origin)
		}
	}
}

func TestPasskeyRoutesRegisteredOnLegacyPrefix(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := gin.New()
	handler.NewAuthHandler().RegisterRoutes(engine.Group("/api/v1"))
	handler.NewAuthHandler().RegisterRoutes(engine.Group("/api"))
	handler.NewSecurityHandler().RegisterRoutes(engine.Group("/api/v1"))
	handler.NewSecurityHandler().RegisterRoutes(engine.Group("/api"))

	user := testutil.MustCreateUser(t, "legacy-passkey", "viewer")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	for _, prefix := range []string{"/api/v1", "/api"} {
		rec := passkeyRequest(engine, http.MethodPost, prefix+"/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, "198.51.100.41")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s login begin: got %d", prefix, rec.Code)
		}
		rec = passkeyRequest(engine, http.MethodGet, prefix+"/security/passkeys", nil, token, "", "198.51.100.41")
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"data":[]}` {
			t.Fatalf("%s list: got %d body=%s", prefix, rec.Code, rec.Body.String())
		}
	}
}

func TestPasskeyRegisterRequiresCurrentPassword(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "passkey-pw-user", "operator", password)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	for _, body := range []string{`{"password":"wrong-password"}`, `{}`} {
		rec := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin", body, token, passkeyTestOrigin, "198.51.100.50")
		if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["error"] != "密码错误" {
			t.Fatalf("body %s: expected 400 密码错误, got %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// 令牌对应的用户不存在 → 404
	ghostToken := testutil.MustCreateAccessToken(t, "ghost-user", "operator")
	rec := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin", `{"password":"x"}`, ghostToken, passkeyTestOrigin, "198.51.100.50")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing user, got %d", rec.Code)
	}

	// 未登录直接拒
	rec = passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin", `{"password":"`+password+`"}`, "", passkeyTestOrigin, "198.51.100.50")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// 密码对但访问地址不支持
	rec = passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin", `{"password":"`+password+`"}`, token, "http://192.168.1.2:5700", "198.51.100.50")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["code"] != "passkey_unsupported_origin" {
		t.Fatalf("expected unsupported origin, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPasskeyRegisterThenLoginMatchesPasswordLogin(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	password := "Password123!"
	// 任何角色都能加自己的通行密钥
	user := testutil.MustCreateLoginUser(t, "passkey-e2e", "viewer", password)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	auth := newTestAuthenticator(t)
	auth.extraFlags = authFlagBE | authFlagBS
	added := mustRegisterPasskey(t, engine, token, password, passkeyTestOrigin, "   ", auth)
	if got := sortedKeys(added); strings.Join(got, ",") != "backup_state,created_at,id,last_used_at,name,origin,rp_id" {
		t.Fatalf("passkey dict must only expose public fields, got %v", got)
	}
	if added["name"] != "通行密钥" || added["rp_id"] != passkeyTestRPID || added["origin"] != passkeyTestOrigin || added["backup_state"] != true || added["last_used_at"] != nil {
		t.Fatalf("unexpected added passkey %#v", added)
	}

	// 账号被别人试密码锁住时，本人仍能用通行密钥登录（登录锁定不拦通行密钥）
	lockedAt := time.Now()
	if err := database.DB.Create(&model.LoginAttempt{
		IP: "198.51.100.60", Username: user.Username, Count: 99, LockedAt: &lockedAt, ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, "198.51.100.60")
	finishBody := map[string]interface{}{
		"session_id": sessionID,
		"credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{}),
	}
	rec := passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/finish", finishBody, "", passkeyTestOrigin, "198.51.100.60")
	if rec.Code != http.StatusOK {
		t.Fatalf("passkey login: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	passkeyPayload := decodeJSONMap(t, rec)

	// 签出的令牌可用，会话行已落库（要在下面的密码登录之前查：同类型客户端的新登录会顶掉旧会话）
	accessToken, _ := passkeyPayload["access_token"].(string)
	if refresh, _ := passkeyPayload["refresh_token"].(string); accessToken == "" || refresh == "" {
		t.Fatalf("missing tokens: %s", rec.Body.String())
	}
	if me := performRequest(engine, http.MethodGet, "/api/v1/auth/user", map[string]string{"Authorization": "Bearer " + accessToken}); me.Code != http.StatusOK {
		t.Fatalf("access token from passkey login should work, got %d", me.Code)
	}
	var sessions int64
	database.DB.Model(&model.UserSession{}).Where("user_id = ? AND ip = ?", user.ID, "198.51.100.60").Count(&sessions)
	if sessions != 1 {
		t.Fatalf("expected one session row for the passkey login, got %d", sessions)
	}
	// 与密码登录响应逐字段同形
	pwRec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", `{"username":"`+user.Username+`","password":"`+password+`"}`, nil, "198.51.100.61")
	if pwRec.Code != http.StatusOK {
		t.Fatalf("password login: expected 200, got %d body=%s", pwRec.Code, pwRec.Body.String())
	}
	pwPayload := decodeJSONMap(t, pwRec)
	if strings.Join(sortedKeys(passkeyPayload), ",") != strings.Join(sortedKeys(pwPayload), ",") {
		t.Fatalf("response keys differ: passkey %v vs password %v", sortedKeys(passkeyPayload), sortedKeys(pwPayload))
	}
	if passkeyPayload["message"] != "登录成功" {
		t.Fatalf("unexpected message %#v", passkeyPayload["message"])
	}
	pkUser, _ := passkeyPayload["user"].(map[string]interface{})
	pwUser, _ := pwPayload["user"].(map[string]interface{})
	if strings.Join(sortedKeys(pkUser), ",") != strings.Join(sortedKeys(pwUser), ",") || pkUser["username"] != user.Username {
		t.Fatalf("user dict differs: %#v vs %#v", pkUser, pwUser)
	}

	// 登录日志：通行密钥那条写明方式；密码登录那条仍是默认的「密码登录」
	var logs []model.LoginLog
	database.DB.Where("user_id = ? AND status = 0", user.ID).Order("id ASC").Find(&logs)
	if len(logs) != 2 {
		t.Fatalf("expected 2 success logs, got %#v", logs)
	}
	if logs[0].Message != "登录成功（通行密钥）" || logs[0].Method != "通行密钥" || logs[0].IP != "198.51.100.60" {
		t.Fatalf("unexpected passkey login log %#v", logs[0])
	}
	if logs[1].Message != "登录成功" || logs[1].Method != "密码登录" {
		t.Fatalf("password login log must stay unchanged, got %#v", logs[1])
	}

	// 成功后清掉这个 IP + 用户名的失败计数（与密码登录收尾相同）
	var attempts int64
	database.DB.Model(&model.LoginAttempt{}).Where("ip = ? AND username = ?", "198.51.100.60", user.Username).Count(&attempts)
	if attempts != 0 {
		t.Fatalf("login attempts should be cleared after passkey login, got %d", attempts)
	}

	// sign count / 最近使用时间回写
	var row model.Passkey
	database.DB.Where("user_id = ?", user.ID).First(&row)
	if row.LastUsedAt == nil {
		t.Fatalf("last_used_at should be set")
	}
	if row.Origin != passkeyTestOrigin {
		t.Fatalf("origin should be stored at registration, got %q", row.Origin)
	}
	var stored struct {
		Authenticator struct {
			SignCount uint32 `json:"signCount"`
		} `json:"authenticator"`
	}
	if err := json.Unmarshal([]byte(row.Credential), &stored); err != nil || stored.Authenticator.SignCount != 1 {
		t.Fatalf("sign count should be written back to 1, got %d (%v) json=%s", stored.Authenticator.SignCount, err, row.Credential)
	}
	var lastUser model.User
	database.DB.First(&lastUser, user.ID)
	if lastUser.LastLoginAt == nil {
		t.Fatalf("last_login_at should be updated")
	}

	// 会话一次性：同一份 finish 原样重放 → 已过期
	replay := passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/finish", finishBody, "", passkeyTestOrigin, "198.51.100.62")
	if replay.Code != http.StatusBadRequest || decodeJSONMap(t, replay)["code"] != "passkey_session_expired" {
		t.Fatalf("replayed finish must be rejected as expired, got %d %s", replay.Code, replay.Body.String())
	}

	// 列表只有自己这一条，最近使用已有值
	list := passkeyRequest(engine, http.MethodGet, "/api/v1/security/passkeys", nil, token, "", "198.51.100.63")
	items, _ := decodeJSONMap(t, list)["data"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 passkey in list, got %s", list.Body.String())
	}
	if item, _ := items[0].(map[string]interface{}); item["last_used_at"] == nil {
		t.Fatalf("list should show last_used_at after login: %s", list.Body.String())
	}
}

func TestPasskeyLoginFinishFailures(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "passkey-fail", "admin", password)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	auth := newTestAuthenticator(t)
	mustRegisterPasskey(t, newProtectedRouter(), token, password, passkeyTestOrigin, "笔记本", auth)

	ipSeq := 0
	nextIP := func() string {
		ipSeq++
		return fmt.Sprintf("203.0.113.%d", ipSeq)
	}
	finish := func(engine *gin.Engine, body interface{}) *httptest.ResponseRecorder {
		return passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/finish", body, "", passkeyTestOrigin, nextIP())
	}
	expectInvalid := func(t *testing.T, rec *httptest.ResponseRecorder, wantLogUser string) {
		t.Helper()
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
		}
		payload := decodeJSONMap(t, rec)
		if payload["code"] != "invalid_passkey" || payload["error"] != "通行密钥验证失败，请重试或改用密码登录" {
			t.Fatalf("unexpected body %s", rec.Body.String())
		}
		log := lastLoginLog(t)
		if log.Status != 1 || log.Message != "通行密钥登录失败" || log.Method != "通行密钥" || log.Username != wantLogUser || log.UserID != 0 {
			t.Fatalf("unexpected failure log %#v", log)
		}
	}

	t.Run("unknown session", func(t *testing.T) {
		rec := finish(newProtectedRouter(), map[string]interface{}{"session_id": "nope", "credential": map[string]interface{}{}})
		if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["code"] != "passkey_session_expired" {
			t.Fatalf("expected session expired, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("register session cannot finish a login", func(t *testing.T) {
		engine := newProtectedRouter()
		sessionID, _ := beginPasskey(t, engine, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, token, passkeyTestOrigin, nextIP())
		rec := finish(engine, map[string]interface{}{"session_id": sessionID, "credential": map[string]interface{}{}})
		if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["code"] != "passkey_session_expired" {
			t.Fatalf("expected session expired, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown credential", func(t *testing.T) {
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		stranger := newTestAuthenticator(t)
		stranger.userHandle = auth.userHandle
		expectInvalid(t, finish(engine, map[string]interface{}{"session_id": sessionID, "credential": stranger.assert(t, options, passkeyTestOrigin, assertionOptions{})}), "")
	})

	t.Run("rp id mismatch", func(t *testing.T) {
		// 在 panel.example.com 下加的密钥，拿到 other.example.com 的会话里用（认证器也按 other 签名，库校验能过，只有 RPID 绑定拦得住）
		engine := newProtectedRouter()
		other := "https://other.example.com"
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", other, nextIP())
		rec := passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/finish", map[string]interface{}{
			"session_id": sessionID,
			"credential": auth.assert(t, options, other, assertionOptions{}),
		}, "", other, nextIP())
		expectInvalid(t, rec, "")
	})

	t.Run("origin mismatch on the same rp id", func(t *testing.T) {
		// 同一个域名换个端口：RP ID 相同，认证器照常签名，库按本次会话的来源校验也能过，只有来源绑定拦得住
		engine := newProtectedRouter()
		otherPort := "https://panel.example.com:8443"
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", otherPort, nextIP())
		if options["rpId"] != passkeyTestRPID {
			t.Fatalf("same host should derive the same rp id, got %#v", options["rpId"])
		}
		rec := passkeyRequest(engine, http.MethodPost, "/api/v1/auth/passkey/login/finish", map[string]interface{}{
			"session_id": sessionID,
			"credential": auth.assert(t, options, otherPort, assertionOptions{}),
		}, "", otherPort, nextIP())
		expectInvalid(t, rec, "")
	})

	t.Run("user handle mismatch", func(t *testing.T) {
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		expectInvalid(t, finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{userHandle: []byte("someone-else-handle")})}), "")
	})

	t.Run("bad signature", func(t *testing.T) {
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		expectInvalid(t, finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{tamper: true})}), user.Username)
	})

	t.Run("user verification required", func(t *testing.T) {
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		expectInvalid(t, finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{flags: authFlagUP})}), user.Username)
	})

	t.Run("disabled user", func(t *testing.T) {
		if err := database.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable user: %v", err)
		}
		defer database.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("enabled", true)
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		rec := finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{})})
		if rec.Code != http.StatusForbidden || decodeJSONMap(t, rec)["error"] != "账号已被禁用" {
			t.Fatalf("expected 403 账号已被禁用, got %d %s", rec.Code, rec.Body.String())
		}
		var sessions int64
		database.DB.Model(&model.UserSession{}).Where("user_id = ?", user.ID).Count(&sessions)
		if sessions != 0 {
			t.Fatalf("disabled user must not get a session, got %d", sessions)
		}
	})

	t.Run("ip whitelist", func(t *testing.T) {
		if err := database.DB.Create(&model.IPWhitelist{IP: "192.0.2.0/24"}).Error; err != nil {
			t.Fatalf("seed whitelist: %v", err)
		}
		defer database.DB.Where("1 = 1").Delete(&model.IPWhitelist{})
		engine := newProtectedRouter()
		sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
		rec := finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{})})
		if rec.Code != http.StatusForbidden || decodeJSONMap(t, rec)["error"] != "当前 IP 不在登录白名单中" {
			t.Fatalf("expected whitelist 403, got %d %s", rec.Code, rec.Body.String())
		}
		if log := lastLoginLog(t); log.Message != "IP 不在白名单" || log.Status != 1 {
			t.Fatalf("unexpected whitelist log %#v", log)
		}
	})

	// 各失败分支之后，正常的通行密钥登录仍然可用（失败不会把凭据或账号弄坏）
	engine := newProtectedRouter()
	sessionID, options := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, nextIP())
	if rec := finish(engine, map[string]interface{}{"session_id": sessionID, "credential": auth.assert(t, options, passkeyTestOrigin, assertionOptions{})}); rec.Code != http.StatusOK {
		t.Fatalf("expected success after failures, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPasskeyRegisterFinishRules(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	password := "Password123!"
	alice := testutil.MustCreateLoginUser(t, "passkey-alice", "operator", password)
	aliceToken := testutil.MustCreateAccessToken(t, alice.Username, alice.Role)
	bob := testutil.MustCreateLoginUser(t, "passkey-bob", "operator", password)
	bobToken := testutil.MustCreateAccessToken(t, bob.Username, bob.Role)

	first := newTestAuthenticator(t)
	longName := strings.Repeat("长", 70)
	added := mustRegisterPasskey(t, engine, aliceToken, password, passkeyTestOrigin, longName, first)
	if added["name"] != strings.Repeat("长", 64) {
		t.Fatalf("name should be cut to 64 characters, got %q", added["name"])
	}

	// 第二次添加：句柄复用、excludeCredentials 只列本 RP ID 下已有的
	sessionID, options := beginPasskey(t, engine, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, aliceToken, passkeyTestOrigin, "198.51.100.70")
	userEntity, _ := options["user"].(map[string]interface{})
	if userEntity["id"] != b64url.EncodeToString(first.userHandle) || userEntity["name"] != alice.Username || userEntity["displayName"] != alice.Username {
		t.Fatalf("user entity should reuse the handle and use the username, got %#v", userEntity)
	}
	excluded, _ := options["excludeCredentials"].([]interface{})
	if len(excluded) != 1 {
		t.Fatalf("expected 1 excluded credential, got %#v", options["excludeCredentials"])
	}
	selection, _ := options["authenticatorSelection"].(map[string]interface{})
	if selection["residentKey"] != "required" || selection["requireResidentKey"] != true || selection["userVerification"] != "required" || options["attestation"] != "none" {
		t.Fatalf("unexpected registration options %#v", options)
	}

	// 同一个认证器再加一次 → 已经添加过了
	rec := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/finish", map[string]interface{}{
		"session_id": sessionID, "name": "重复", "credential": first.register(t, options, passkeyTestOrigin),
	}, aliceToken, passkeyTestOrigin, "198.51.100.70")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["error"] != "这个通行密钥已经添加过了" {
		t.Fatalf("expected duplicate rejection, got %d %s", rec.Code, rec.Body.String())
	}

	// 别的域名下不排除这一枚
	_, otherOptions := beginPasskey(t, engine, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, aliceToken, "https://other.example.com", "198.51.100.71")
	if _, exists := otherOptions["excludeCredentials"]; exists {
		t.Fatalf("credentials of another RP ID must not be excluded: %#v", otherOptions["excludeCredentials"])
	}

	// 会话属于 alice，bob 拿去 finish → 按已过期处理，且会话已被消耗
	aliceSession, aliceOptions := beginPasskey(t, engine, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, aliceToken, passkeyTestOrigin, "198.51.100.72")
	stolen := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/finish", map[string]interface{}{
		"session_id": aliceSession, "credential": newTestAuthenticator(t).register(t, aliceOptions, passkeyTestOrigin),
	}, bobToken, passkeyTestOrigin, "198.51.100.72")
	if stolen.Code != http.StatusBadRequest || decodeJSONMap(t, stolen)["code"] != "passkey_session_expired" {
		t.Fatalf("expected session expired for another user's session, got %d %s", stolen.Code, stolen.Body.String())
	}
	var bobCount int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", bob.ID).Count(&bobCount)
	if bobCount != 0 {
		t.Fatalf("bob must not get a passkey from alice's session")
	}

	// 登录会话不能拿来完成注册
	loginSession, loginOptions := beginPasskey(t, engine, "/api/v1/auth/passkey/login/begin", `{}`, "", passkeyTestOrigin, "198.51.100.73")
	_ = loginOptions
	wrongPurpose := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/finish", map[string]interface{}{
		"session_id": loginSession, "credential": map[string]interface{}{},
	}, aliceToken, passkeyTestOrigin, "198.51.100.73")
	if wrongPurpose.Code != http.StatusBadRequest || decodeJSONMap(t, wrongPurpose)["code"] != "passkey_session_expired" {
		t.Fatalf("expected session expired for a login session, got %d %s", wrongPurpose.Code, wrongPurpose.Body.String())
	}

	// 上限 20 个
	for i := 0; i < 19; i++ {
		if err := database.DB.Create(&model.Passkey{
			UserID: alice.ID, Name: "占位", RPID: passkeyTestRPID,
			CredentialID: fmt.Sprintf("filler-%d", i), UserHandle: b64url.EncodeToString(first.userHandle), Credential: "{}",
		}).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
	}
	full := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin", map[string]string{"password": password}, aliceToken, passkeyTestOrigin, "198.51.100.74")
	if full.Code != http.StatusBadRequest || decodeJSONMap(t, full)["error"] != "每个账号最多添加 20 个通行密钥" {
		t.Fatalf("expected limit rejection, got %d %s", full.Code, full.Body.String())
	}
}

func TestPasskeyManageOnlyOwn(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	alice := testutil.MustCreateUser(t, "pk-owner-alice", "admin")
	aliceToken := testutil.MustCreateAccessToken(t, alice.Username, alice.Role)
	bob := testutil.MustCreateUser(t, "pk-owner-bob", "viewer")
	bobToken := testutil.MustCreateAccessToken(t, bob.Username, bob.Role)

	seed := func(userID uint, credentialID, name string) model.Passkey {
		row := model.Passkey{UserID: userID, Name: name, RPID: passkeyTestRPID, CredentialID: credentialID, UserHandle: "aGFuZGxl", Credential: "{}"}
		if err := database.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
		return row
	}
	aliceKey := seed(alice.ID, "alice-cred", "alice 的")
	bobKey := seed(bob.ID, "bob-cred", "bob 的")

	// 管理员也动不了别人的：一律 404
	path := fmt.Sprintf("/api/v1/security/passkeys/%d", bobKey.ID)
	if rec := passkeyRequest(engine, http.MethodPut, path, `{"name":"改掉"}`, aliceToken, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("rename someone else's passkey: expected 404, got %d", rec.Code)
	}
	if rec := passkeyRequest(engine, http.MethodDelete, path, nil, aliceToken, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete someone else's passkey: expected 404, got %d", rec.Code)
	}
	var stillThere model.Passkey
	if err := database.DB.First(&stillThere, bobKey.ID).Error; err != nil || stillThere.Name != "bob 的" {
		t.Fatalf("bob's passkey must stay untouched: %#v %v", stillThere, err)
	}

	// 列表只有自己的
	list := passkeyRequest(engine, http.MethodGet, "/api/v1/security/passkeys", nil, bobToken, "", "")
	items, _ := decodeJSONMap(t, list)["data"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("bob should only see his own passkey: %s", list.Body.String())
	}
	if item, _ := items[0].(map[string]interface{}); item["name"] != "bob 的" {
		t.Fatalf("unexpected list item %#v", item)
	}

	// 自己的：viewer 也能改名、删除
	ownPath := fmt.Sprintf("/api/v1/security/passkeys/%d", bobKey.ID)
	rec := passkeyRequest(engine, http.MethodPut, ownPath, `{"name":"  手机  "}`, bobToken, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "已重命名" {
		t.Fatalf("rename own: got %d %s", rec.Code, rec.Body.String())
	}
	database.DB.First(&stillThere, bobKey.ID)
	if stillThere.Name != "手机" {
		t.Fatalf("name should be trimmed, got %q", stillThere.Name)
	}
	rec = passkeyRequest(engine, http.MethodDelete, ownPath, nil, bobToken, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "已删除" {
		t.Fatalf("delete own: got %d %s", rec.Code, rec.Body.String())
	}
	var remaining int64
	database.DB.Model(&model.Passkey{}).Count(&remaining)
	if remaining != 1 {
		t.Fatalf("only bob's passkey should be gone, %d left", remaining)
	}
	if err := database.DB.First(&model.Passkey{}, aliceKey.ID).Error; err != nil {
		t.Fatalf("alice's passkey must remain: %v", err)
	}
}

func TestDeleteUserAlsoDeletesPasskeys(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := gin.New()
	handler.NewUserHandler().RegisterRoutes(engine.Group("/api/v1"))

	admin := testutil.MustCreateUser(t, "pk-del-admin", "admin")
	adminToken := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	victim := testutil.MustCreateUser(t, "pk-del-victim", "operator")
	for i, owner := range []uint{victim.ID, victim.ID, admin.ID} {
		if err := database.DB.Create(&model.Passkey{UserID: owner, Name: "k", RPID: passkeyTestRPID, CredentialID: fmt.Sprintf("del-%d", i), UserHandle: "aA", Credential: "{}"}).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
	}

	rec := passkeyRequest(engine, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", victim.ID), nil, adminToken, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete user: got %d %s", rec.Code, rec.Body.String())
	}
	var victimKeys, adminKeys int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", victim.ID).Count(&victimKeys)
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", admin.ID).Count(&adminKeys)
	if victimKeys != 0 || adminKeys != 1 {
		t.Fatalf("expected victim's passkeys removed and admin's kept, got %d / %d", victimKeys, adminKeys)
	}
}

// 开了两步验证的账号添加通行密钥：先校验密码，再要两步验证码；没开的账号行为不变。
func TestPasskeyRegisterRequiresTOTPWhenTwoFactorEnabled(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "passkey-2fa-user", "operator", password)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	secret := mustEnableTwoFactor(t, user.ID)
	beginPath := "/api/v1/security/passkeys/register/begin"

	expect := func(t *testing.T, body map[string]string, wantError, wantCode string) {
		t.Helper()
		rec := passkeyRequest(engine, http.MethodPost, beginPath, body, token, passkeyTestOrigin, "198.51.100.90")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %v: expected 400, got %d %s", body, rec.Code, rec.Body.String())
		}
		payload := decodeJSONMap(t, rec)
		if payload["error"] != wantError || (wantCode != "" && payload["code"] != wantCode) {
			t.Fatalf("body %v: unexpected response %s", body, rec.Body.String())
		}
	}

	// 没带验证码
	expect(t, map[string]string{"password": password}, "请输入两步验证码", "two_factor_required")
	expect(t, map[string]string{"password": password, "totp_code": "   "}, "请输入两步验证码", "two_factor_required")
	// 验证码错
	expect(t, map[string]string{"password": password, "totp_code": mustInvalidTOTPCode(t, secret)}, "两步验证码错误", "invalid_totp")
	// 先密码、再验证码：密码错时即使验证码对也只报密码错误
	expect(t, map[string]string{"password": "wrong-password", "totp_code": service.GenerateCurrentTOTPForTest(secret)}, "密码错误", "")

	// 密码与验证码都对 → 能走完整个添加流程
	sessionID, options := beginPasskey(t, engine, beginPath, map[string]string{"password": password, "totp_code": service.GenerateCurrentTOTPForTest(secret)}, token, passkeyTestOrigin, "198.51.100.90")
	rec := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/finish", map[string]interface{}{
		"session_id": sessionID, "name": "2FA 账号的", "credential": newTestAuthenticator(t).register(t, options, passkeyTestOrigin),
	}, token, passkeyTestOrigin, "198.51.100.90")
	if rec.Code != http.StatusOK {
		t.Fatalf("register finish with 2FA: got %d %s", rec.Code, rec.Body.String())
	}

	// 没开两步验证的账号：只要密码，带不带 totp_code 都照常
	plain := testutil.MustCreateLoginUser(t, "passkey-no-2fa-user", "operator", password)
	plainToken := testutil.MustCreateAccessToken(t, plain.Username, plain.Role)
	for _, body := range []map[string]string{{"password": password}, {"password": password, "totp_code": "000000"}} {
		if rec := passkeyRequest(engine, http.MethodPost, beginPath, body, plainToken, passkeyTestOrigin, "198.51.100.91"); rec.Code != http.StatusOK {
			t.Fatalf("user without 2FA, body %v: expected 200, got %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

// 登录 begin 按客户端 IP 限额：同一个 IP 连发 11 次都成功，最早那次的会话被挤掉；别的 IP 的会话不受影响。
func TestPasskeyLoginBeginPerIPEvictsOwnOldest(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	beginPath := "/api/v1/auth/passkey/login/begin"
	finishPath := "/api/v1/auth/passkey/login/finish"

	victimSession, _ := beginPasskey(t, engine, beginPath, `{}`, "", passkeyTestOrigin, "198.51.100.95")
	var sessions []string
	for i := 0; i < 11; i++ {
		sessionID, _ := beginPasskey(t, engine, beginPath, `{}`, "", passkeyTestOrigin, "198.51.100.96")
		sessions = append(sessions, sessionID)
	}

	// 最早那次已被挤掉：finish 按已过期处理
	rec := passkeyRequest(engine, http.MethodPost, finishPath, map[string]interface{}{"session_id": sessions[0], "credential": map[string]interface{}{}}, "", passkeyTestOrigin, "198.51.100.96")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["code"] != "passkey_session_expired" {
		t.Fatalf("oldest session of the IP should be evicted, got %d %s", rec.Code, rec.Body.String())
	}
	// 第二早的还在：会话被取出后凭据不对 → 通行密钥验证失败（不是已过期）
	rec = passkeyRequest(engine, http.MethodPost, finishPath, map[string]interface{}{"session_id": sessions[1], "credential": map[string]interface{}{}}, "", passkeyTestOrigin, "198.51.100.96")
	if rec.Code != http.StatusUnauthorized || decodeJSONMap(t, rec)["code"] != "invalid_passkey" {
		t.Fatalf("second oldest session should still exist, got %d %s", rec.Code, rec.Body.String())
	}
	// 另一个 IP 的会话不受影响
	rec = passkeyRequest(engine, http.MethodPost, finishPath, map[string]interface{}{"session_id": victimSession, "credential": map[string]interface{}{}}, "", passkeyTestOrigin, "198.51.100.95")
	if rec.Code != http.StatusUnauthorized || decodeJSONMap(t, rec)["code"] != "invalid_passkey" {
		t.Fatalf("another IP's session must survive, got %d %s", rec.Code, rec.Body.String())
	}
}

// seedPasskeys 给用户塞 n 枚通行密钥（只用于计数，不用于登录）
func seedPasskeys(t *testing.T, userID uint, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := database.DB.Create(&model.Passkey{
			UserID: userID, Name: "k", RPID: passkeyTestRPID, Origin: passkeyTestOrigin,
			CredentialID: fmt.Sprintf("%s-%d", prefix, i), UserHandle: "aA", Credential: "{}",
		}).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
	}
}

func countPasskeys(userID uint) int64 {
	var count int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", userID).Count(&count)
	return count
}

// 自己改密：通行密钥一并清除，清掉了才在提示里说个数；改密失败不动通行密钥；只清自己的。
func TestChangePasswordClearsOwnPasskeys(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "pw-change-pk", "operator", password)
	other := testutil.MustCreateUser(t, "pw-change-other", "operator")
	seedPasskeys(t, user.ID, "pw-change-own", 2)
	seedPasskeys(t, other.ID, "pw-change-other", 1)

	// 旧密码不对：失败，通行密钥原样保留
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	rec := passkeyRequest(engine, http.MethodPut, "/api/v1/auth/password", map[string]string{"old_password": "wrong-password", "new_password": "NewPassword456!"}, token, "", "")
	if rec.Code != http.StatusBadRequest || countPasskeys(user.ID) != 2 {
		t.Fatalf("failed change must keep passkeys: %d %s, left %d", rec.Code, rec.Body.String(), countPasskeys(user.ID))
	}

	rec = passkeyRequest(engine, http.MethodPut, "/api/v1/auth/password", map[string]string{"old_password": password, "new_password": "NewPassword456!"}, token, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "密码修改成功，已添加的 2 个通行密钥也已清除，请重新登录" {
		t.Fatalf("change password: got %d %s", rec.Code, rec.Body.String())
	}
	if countPasskeys(user.ID) != 0 || countPasskeys(other.ID) != 1 {
		t.Fatalf("expected own passkeys cleared and others kept, got %d / %d", countPasskeys(user.ID), countPasskeys(other.ID))
	}

	// 没有通行密钥时文案不变
	token = testutil.MustCreateAccessToken(t, user.Username, user.Role)
	rec = passkeyRequest(engine, http.MethodPut, "/api/v1/auth/password", map[string]string{"old_password": "NewPassword456!", "new_password": "NewPassword789!"}, token, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "密码修改成功，请重新登录" {
		t.Fatalf("change password without passkeys: got %d %s", rec.Code, rec.Body.String())
	}
}

// 管理员重置别人的密码：对方的通行密钥一并清除，提示里补一句个数；没有时文案不变。
func TestAdminResetPasswordClearsTargetPasskeys(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := gin.New()
	handler.NewUserHandler().RegisterRoutes(engine.Group("/api/v1"))

	admin := testutil.MustCreateUser(t, "pw-reset-admin", "admin")
	adminToken := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	target := testutil.MustCreateUser(t, "pw-reset-target", "operator")
	seedPasskeys(t, target.ID, "pw-reset-target", 3)
	seedPasskeys(t, admin.ID, "pw-reset-admin", 1)

	path := fmt.Sprintf("/api/v1/users/%d/reset-password", target.ID)
	rec := passkeyRequest(engine, http.MethodPut, path, map[string]string{"password": "NewPassword456!"}, adminToken, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "密码重置成功，该用户已添加的 3 个通行密钥也已清除" {
		t.Fatalf("reset password: got %d %s", rec.Code, rec.Body.String())
	}
	if countPasskeys(target.ID) != 0 || countPasskeys(admin.ID) != 1 {
		t.Fatalf("expected target's passkeys cleared and admin's kept, got %d / %d", countPasskeys(target.ID), countPasskeys(admin.ID))
	}

	rec = passkeyRequest(engine, http.MethodPut, path, map[string]string{"password": "NewPassword789!"}, adminToken, "", "")
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["message"] != "密码重置成功" {
		t.Fatalf("reset password without passkeys: got %d %s", rec.Code, rec.Body.String())
	}
}
