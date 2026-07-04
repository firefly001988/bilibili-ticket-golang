package cluster_service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/global"
	api "bilibili-ticket-golang/lib/models/bili/api"
)

type accountLoginSession struct {
	Client                 *biliutils.BiliClient
	Jar                    *cookiejar.Jar
	QRCodeKey              string
	CaptchaKey             string
	Name                   string
	SafecenterRequestID    string
	SafecenterTmpToken     string
	SafecenterCaptchaToken string
	CreatedAt              time.Time
}

// AccountLoginStart is returned by BeginAccountLogin and BeginAccountSMSLogin.
type AccountLoginStart struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
}

// AccountLoginPoll is returned by PollAccountLogin.
type AccountLoginPoll struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	AccountID string `json:"accountId,omitempty"`
}

// AccountLoginResult is returned after a non-QR login is persisted.
type AccountLoginResult struct {
	AccountID                string `json:"accountId"`
	Name                     string `json:"name"`
	Status                   int    `json:"status"`
	Message                  string `json:"message,omitempty"`
	URL                      string `json:"url,omitempty"`
	SessionID                string `json:"sessionId,omitempty"`
	NeedSafecenterVerify     bool   `json:"needSafecenterVerify,omitempty"`
	SafecenterSMSSent        bool   `json:"safecenterSmsSent,omitempty"`
	SafecenterCaptchaSession string `json:"safecenterCaptchaSession,omitempty"`
}

// LoginCaptchaPrepareResult is returned by PrepareLoginCaptcha.
// The frontend uses gt + challenge to render the geetest captcha widget;
// sessionId is passed back to the login methods.
type LoginCaptchaPrepareResult struct {
	SessionID string `json:"sessionId"`
	Gt        string `json:"gt"`
	Challenge string `json:"challenge"`
}

// loginCaptchaSession holds the transient state for a login captcha
// flow. The BiliClient and cookie jar are preserved so that the
// same HTTP session is used across the Prepare → Solve → Login steps.
type loginCaptchaSession struct {
	Client    *biliutils.BiliClient
	Jar       *cookiejar.Jar
	Token     string
	Gt        string
	Challenge string
	CreatedAt time.Time
}

// BeginAccountLogin starts a QR login session for a new account.
func (s *ClusterService) BeginAccountLogin(name string) (AccountLoginStart, error) {
	jar := cookiejar.New(nil)
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return AccountLoginStart{}, err
	}
	qr, err := client.GetQRCodeUrlAndKey()
	if err != nil {
		return AccountLoginStart{}, err
	}
	if qr == nil || qr.URL == "" || qr.QRCodeKey == "" {
		return AccountLoginStart{}, fmt.Errorf("bilibili returned an empty login QR code")
	}
	id := randomClusterID("login")
	s.mu.Lock()
	s.loginSessions[id] = &accountLoginSession{Client: client, Jar: jar, QRCodeKey: qr.QRCodeKey, Name: name, CreatedAt: time.Now()}
	s.mu.Unlock()
	return AccountLoginStart{SessionID: id, URL: qr.URL}, nil
}

// PollAccountLogin checks the QR login session state. On success it persists
// the new account and imports its buyers.
func (s *ClusterService) PollAccountLogin(sessionID string) (AccountLoginPoll, error) {
	s.mu.RLock()
	session := s.loginSessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return AccountLoginPoll{}, fmt.Errorf("login session not found")
	}
	if time.Since(session.CreatedAt) > 5*time.Minute {
		s.mu.Lock()
		delete(s.loginSessions, sessionID)
		s.mu.Unlock()
		return AccountLoginPoll{}, fmt.Errorf("login session expired")
	}
	state, err := session.Client.GetQRLoginState(session.QRCodeKey)
	if err != nil {
		return AccountLoginPoll{}, err
	}
	result := AccountLoginPoll{Code: state.Code, Message: state.Message}
	if state.Code != 0 {
		return result, nil
	}
	session.Client.SetRefreshToken(state.RefreshToken)
	saved, err := s.persistLoggedInAccount(session.Client, session.Jar, session.Name, "QR login")
	if err != nil {
		return result, err
	}
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	result.AccountID = saved.AccountID
	return result, nil
}

// BeginAccountSMSLogin sends a Bilibili SMS verification code and keeps the
// transient client session needed to verify it.
//
// When captchaSessionId is non‑empty, the captcha from the prepared session
// is used instead of the DLL solver.  The frontend obtains captchaSessionId,
// challenge and validate via PrepareLoginCaptcha + the geetest widget.
func (s *ClusterService) BeginAccountSMSLogin(phone string, cid int64, name string, captchaSessionId string, challenge string, validate string) (AccountLoginStart, error) {
	if phone == "" {
		return AccountLoginStart{}, global.NewFault("发送短信验证码", fmt.Errorf("phone is required"), "请填写手机号后再发送验证码")
	}
	if cid == 0 {
		cid = 86
	}
	if captchaSessionId != "" {
		session := s.popLoginCaptchaSession(captchaSessionId)
		if session == nil {
			return AccountLoginStart{}, global.NewFault("发送短信验证码", fmt.Errorf("captcha session not found or expired"), "验证码会话已过期，请重新完成验证码")
		}
		return s.beginAccountSMSLogin(phone, cid, name, session.Client, session.Jar, session.Token, challenge, validate)
	}
	jar := cookiejar.New(nil)
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return AccountLoginStart{}, global.NewFault("发送短信验证码: 创建登录会话", err, "请检查网络连接后重试")
	}
	token, challenge2, validate2, err := s.solveLoginCaptcha(client)
	if err != nil {
		return AccountLoginStart{}, global.NewFault("发送短信验证码: 验证码求解", err, "请确认验证码求解器已安装，或重新打开验证码")
	}
	return s.beginAccountSMSLogin(phone, cid, name, client, jar, token, challenge2, validate2)
}

// beginAccountSMSLogin is the shared SMS login implementation.
func (s *ClusterService) beginAccountSMSLogin(phone string, cid int64, name string, client *biliutils.BiliClient, jar *cookiejar.Jar, token string, challenge string, validate string) (AccountLoginStart, error) {
	captchaKey, err := client.SendSMSCode(parsePhoneForLogin(phone), cid, token, validate, challenge)
	if err != nil {
		return AccountLoginStart{}, global.NewFault("发送短信验证码", err, "请确认手机号和区号正确，稍后重试")
	}
	id := randomClusterID("sms-login")
	s.mu.Lock()
	s.loginSessions[id] = &accountLoginSession{Client: client, Jar: jar, CaptchaKey: captchaKey, Name: name, CreatedAt: time.Now()}
	s.mu.Unlock()
	return AccountLoginStart{SessionID: id}, nil
}

// FinishAccountSMSLogin verifies an SMS code, persists the logged-in account,
// and imports its buyers.
func (s *ClusterService) FinishAccountSMSLogin(sessionID string, phone string, cid int64, smsCode string) (AccountLoginResult, error) {
	if phone == "" || smsCode == "" {
		return AccountLoginResult{}, global.NewFault("短信验证码登录", fmt.Errorf("phone and SMS code are required"), "请填写手机号和短信验证码")
	}
	if cid == 0 {
		cid = 86
	}
	s.mu.RLock()
	session := s.loginSessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return AccountLoginResult{}, global.NewFault("短信验证码登录", fmt.Errorf("login session not found"), "登录会话不存在，请重新发送短信验证码")
	}
	if time.Since(session.CreatedAt) > 5*time.Minute {
		s.mu.Lock()
		delete(s.loginSessions, sessionID)
		s.mu.Unlock()
		return AccountLoginResult{}, global.NewFault("短信验证码登录", fmt.Errorf("login session expired"), "登录会话已过期，请重新发送短信验证码")
	}
	if _, err := session.Client.VerifySMSCode(parsePhoneForLogin(phone), cid, session.CaptchaKey, smsCode); err != nil {
		return AccountLoginResult{}, global.NewFault("短信验证码登录", err, "请确认短信验证码正确且未过期")
	}
	result, err := s.persistLoggedInAccount(session.Client, session.Jar, session.Name, "SMS login")
	if err != nil {
		return AccountLoginResult{}, global.NewFault("保存短信登录账号", err, "登录成功但保存账号失败，请检查数据库")
	}
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	return result, nil
}

// AccountPasswordLogin logs in with username/password, persists the account,
// and imports its buyers.
//
// When captchaSessionId is non‑empty, the captcha from the prepared session
// is used instead of the DLL solver.  The frontend obtains captchaSessionId,
// challenge, validate and seccode via PrepareLoginCaptcha + the geetest widget.
func (s *ClusterService) AccountPasswordLogin(username string, password string, name string, captchaSessionId string, challenge string, validate string, seccode string) (AccountLoginResult, error) {
	if username == "" || password == "" {
		return AccountLoginResult{}, global.NewFault("密码登录", fmt.Errorf("username and password are required"), "请填写账号和密码")
	}
	if captchaSessionId != "" {
		session := s.popLoginCaptchaSession(captchaSessionId)
		if session == nil {
			return AccountLoginResult{}, global.NewFault("密码登录", fmt.Errorf("captcha session not found or expired"), "验证码会话已过期，请重新完成验证码")
		}
		return s.accountPasswordLogin(session.Client, session.Jar, username, password, name, session.Token, challenge, validate, seccode)
	}
	jar := cookiejar.New(nil)
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("密码登录: 创建登录会话", err, "请检查网络连接后重试")
	}
	token, challenge2, validate2, err := s.solveLoginCaptcha(client)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("密码登录: 验证码求解", err, "请确认验证码求解器已安装，或重新打开验证码")
	}
	return s.accountPasswordLogin(client, jar, username, password, name, token, challenge2, validate2, validate2+"|jordan")
}

// accountPasswordLogin is the shared password login implementation.
func (s *ClusterService) accountPasswordLogin(client *biliutils.BiliClient, jar *cookiejar.Jar, username, password, name, token, challenge, validate, seccode string) (AccountLoginResult, error) {
	salt, pubKey, err := client.GetPasswordKey()
	if err != nil {
		return AccountLoginResult{}, global.NewFault("密码登录: 获取加密密钥", err, "请检查网络连接后重试")
	}
	encrypted, err := biliutils.EncryptPassword(password, salt, pubKey)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("密码登录: 加密密码", err, "密码加密失败，请重试")
	}
	loginResp, err := client.PasswordLogin(username, encrypted, token, challenge, validate, seccode)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("密码登录", err, "请确认账号、密码和验证码正确")
	}
	if loginResp != nil && loginResp.Status == 2 {
		requestID, tmpToken, parseErr := parseSafecenterVerifyURL(loginResp.URL)
		if parseErr != nil {
			return AccountLoginResult{}, global.NewFault("密码登录: 解析安全验证地址", parseErr, "Bilibili 返回的安全验证地址异常，请稍后重试")
		}
		id := randomClusterID("safe-login")
		s.mu.Lock()
		s.loginSessions[id] = &accountLoginSession{
			Client:              client,
			Jar:                 jar,
			Name:                name,
			SafecenterRequestID: requestID,
			SafecenterTmpToken:  tmpToken,
			CreatedAt:           time.Now(),
		}
		s.mu.Unlock()
		return AccountLoginResult{
			Status:               loginResp.Status,
			Message:              firstNonEmpty(loginResp.Message, loginResp.Hint, "需要手机号验证"),
			URL:                  loginResp.URL,
			SessionID:            id,
			NeedSafecenterVerify: true,
		}, nil
	}
	if loginResp != nil && loginResp.Status != 0 {
		return AccountLoginResult{}, global.NewFault("密码登录", errors.New(firstNonEmpty(loginResp.Message, loginResp.Hint, fmt.Sprintf("password login status %d", loginResp.Status))), "请确认账号、密码和验证码正确")
	}
	result, err := s.persistLoggedInAccount(client, jar, name, "password login")
	if err != nil {
		return AccountLoginResult{}, global.NewFault("保存密码登录账号", err, "登录成功但保存账号失败，请检查数据库")
	}
	return result, nil
}

// PrepareSafecenterCaptcha fetches the Geetest challenge for a password-login
// risk verification session.
func (s *ClusterService) PrepareSafecenterCaptcha(sessionID string) (LoginCaptchaPrepareResult, error) {
	session, err := s.getSafecenterSession(sessionID)
	if err != nil {
		return LoginCaptchaPrepareResult{}, global.NewFault("获取安全中心验证码", err, "安全验证会话无效，请重新进行密码登录")
	}
	cpt, err := session.Client.GetSafecenterCaptchaPre()
	if err != nil {
		return LoginCaptchaPrepareResult{}, global.NewFault("获取安全中心验证码", err, "请检查网络连接后重试")
	}
	s.mu.Lock()
	if current := s.loginSessions[sessionID]; current != nil {
		current.SafecenterCaptchaToken = cpt.Token
	}
	s.mu.Unlock()
	return LoginCaptchaPrepareResult{
		SessionID: sessionID,
		Gt:        cpt.Gt,
		Challenge: cpt.Challenge,
	}, nil
}

// SendAccountSafecenterSMSCode sends the phone SMS code for a password-login
// risk verification session. If challenge/validate are empty, it solves the
// captcha with the installed DLL solver.
func (s *ClusterService) SendAccountSafecenterSMSCode(sessionID string, challenge string, validate string) (AccountLoginStart, error) {
	session, err := s.getSafecenterSession(sessionID)
	if err != nil {
		return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码", err, "安全验证会话无效，请重新进行密码登录")
	}
	token := session.SafecenterCaptchaToken
	if validate == "" {
		if s.captchaSolver == nil {
			return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码", fmt.Errorf("captcha solver is not installed"), "请安装验证码求解器，或使用手动验证码")
		}
		cpt, err := session.Client.GetSafecenterCaptchaPre()
		if err != nil {
			return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码: 获取验证码", err, "请检查网络连接后重试")
		}
		token = cpt.Token
		challenge = cpt.Challenge
		validate, err = s.captchaSolver(cpt.Gt, cpt.Challenge)
		if err != nil {
			return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码: 验证码求解", err, "请确认验证码求解器已安装，或重新打开验证码")
		}
	}
	if token == "" {
		return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码", fmt.Errorf("safecenter captcha token is missing"), "验证码会话无效，请重新完成验证码")
	}
	captchaKey, err := session.Client.SendSafecenterSMSCode(session.SafecenterTmpToken, token, challenge, validate)
	if err != nil {
		return AccountLoginStart{}, global.NewFault("发送安全中心短信验证码", err, "请稍后重试，或重新进行密码登录")
	}
	s.mu.Lock()
	if current := s.loginSessions[sessionID]; current != nil {
		current.CaptchaKey = captchaKey
		current.SafecenterCaptchaToken = token
	}
	s.mu.Unlock()
	return AccountLoginStart{SessionID: sessionID}, nil
}

// FinishAccountSafecenterSMSLogin verifies the phone SMS code, exchanges the
// returned oauth code for cookies, persists the account, and imports buyers.
func (s *ClusterService) FinishAccountSafecenterSMSLogin(sessionID string, smsCode string) (AccountLoginResult, error) {
	if smsCode == "" {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证", fmt.Errorf("SMS code is required"), "请填写短信验证码")
	}
	session, err := s.getSafecenterSession(sessionID)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证", err, "安全验证会话无效，请重新进行密码登录")
	}
	if session.CaptchaKey == "" {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证", fmt.Errorf("safecenter SMS code has not been sent"), "请先发送安全中心短信验证码")
	}
	verifyResp, err := session.Client.VerifySafecenterSMSCode(session.SafecenterTmpToken, session.SafecenterCaptchaToken, session.CaptchaKey, session.SafecenterRequestID, smsCode)
	if err != nil {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证", err, "请确认短信验证码正确且未过期")
	}
	if verifyResp == nil || verifyResp.OauthCode == "" {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证", fmt.Errorf("safecenter verify did not return oauth code"), "Bilibili 未返回登录凭证，请重新进行密码登录")
	}
	if _, err = session.Client.ExhangeCookieByOauthCode(verifyResp.OauthCode); err != nil {
		return AccountLoginResult{}, global.NewFault("安全中心短信验证: 换取登录 Cookie", err, "请稍后重试，或重新进行密码登录")
	}
	result, err := s.persistLoggedInAccount(session.Client, session.Jar, session.Name, "password safecenter login")
	if err != nil {
		return AccountLoginResult{}, global.NewFault("保存密码登录账号", err, "登录成功但保存账号失败，请检查数据库")
	}
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	result.Status = 0
	return result, nil
}

func (s *ClusterService) getSafecenterSession(sessionID string) (*accountLoginSession, error) {
	s.mu.RLock()
	session := s.loginSessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("login session not found")
	}
	if time.Since(session.CreatedAt) > 5*time.Minute {
		s.mu.Lock()
		delete(s.loginSessions, sessionID)
		s.mu.Unlock()
		return nil, fmt.Errorf("login session expired")
	}
	if session.SafecenterRequestID == "" || session.SafecenterTmpToken == "" {
		return nil, fmt.Errorf("login session is not waiting for safecenter verification")
	}
	return session, nil
}

func parseSafecenterVerifyURL(rawURL string) (requestID string, tmpToken string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parse safecenter verify url: %w", err)
	}
	values := u.Query()
	requestID = values.Get("request_id")
	tmpToken = values.Get("tmp_token")
	if requestID == "" || tmpToken == "" {
		return "", "", fmt.Errorf("safecenter verify url missing request_id or tmp_token")
	}
	return requestID, tmpToken, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// HasLoginCaptchaSolver returns true if a captcha solver is installed.
// The frontend uses this to decide whether to show the geetest captcha
// widget or let the DLL solve it automatically.
func (s *ClusterService) HasLoginCaptchaSolver() bool {
	return s.captchaSolver != nil
}

// PrepareLoginCaptcha creates a fresh BiliClient, fetches a login captcha
// (gt + challenge) from Bilibili, and stores the session for later use
// by BeginAccountSMSLogin / AccountPasswordLogin.
func (s *ClusterService) PrepareLoginCaptcha() (LoginCaptchaPrepareResult, error) {
	jar := cookiejar.New(nil)
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return LoginCaptchaPrepareResult{}, global.NewFault("准备登录验证码", err, "请检查网络连接后重试")
	}
	cpt, err := client.GetLoginCaptcha()
	if err != nil {
		return LoginCaptchaPrepareResult{}, global.NewFault("准备登录验证码", err, "请检查网络连接后重试")
	}
	id := randomClusterID("captcha")
	s.mu.Lock()
	if s.loginCaptchaSessions == nil {
		s.loginCaptchaSessions = make(map[string]*loginCaptchaSession)
	}
	s.loginCaptchaSessions[id] = &loginCaptchaSession{
		Client:    client,
		Jar:       jar,
		Token:     cpt.Token,
		Gt:        cpt.Geetest.Gt,
		Challenge: cpt.Geetest.Challenge,
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()
	return LoginCaptchaPrepareResult{
		SessionID: id,
		Gt:        cpt.Geetest.Gt,
		Challenge: cpt.Geetest.Challenge,
	}, nil
}

// popLoginCaptchaSession retrieves and deletes a login captcha session.
func (s *ClusterService) popLoginCaptchaSession(sessionID string) *loginCaptchaSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.loginCaptchaSessions[sessionID]
	if session == nil {
		return nil
	}
	if time.Since(session.CreatedAt) > 5*time.Minute {
		delete(s.loginCaptchaSessions, sessionID)
		return nil
	}
	delete(s.loginCaptchaSessions, sessionID)
	return session
}

// GetLoginCountries fetches the country list for SMS login.
// Returns the common and other country entries.
func (s *ClusterService) GetLoginCountries() (*api.CountryListStruct, error) {
	if s.catalog != nil {
		return s.catalog.GetCountryList()
	}
	client, err := biliutils.NewBiliClient()
	if err != nil {
		return nil, err
	}
	return client.GetCountryList()
}

func (s *ClusterService) solveLoginCaptcha(client *biliutils.BiliClient) (token string, challenge string, validate string, err error) {
	if s.captchaSolver == nil {
		return "", "", "", global.NewFault("登录验证码求解", fmt.Errorf("captcha solver is not installed"), "请安装验证码求解器，或使用手动验证码")
	}
	captcha, err := client.GetLoginCaptcha()
	if err != nil {
		return "", "", "", global.NewFault("登录验证码求解: 获取验证码", err, "请检查网络连接后重试")
	}
	validate, err = s.captchaSolver(captcha.Geetest.Gt, captcha.Geetest.Challenge)
	if err != nil {
		return "", "", "", global.NewFault("登录验证码求解", err, "请确认验证码求解器已安装，或重新打开验证码")
	}
	return captcha.Token, captcha.Geetest.Challenge, validate, nil
}

func (s *ClusterService) persistLoggedInAccount(client *biliutils.BiliClient, jar *cookiejar.Jar, requestedName string, source string) (AccountLoginResult, error) {
	info, err := client.GetAccountStatus()
	if err != nil {
		return AccountLoginResult{}, err
	}
	if info == nil || !info.Login || info.UID == 0 {
		return AccountLoginResult{}, fmt.Errorf("login did not produce a valid account session")
	}
	profile, _ := json.Marshal(client.ExportDeviceProfile())
	credentials := credentialsFrom(client, jar, domain.Credentials{RefreshToken: client.GetRefreshToken(), Version: 1, DeviceProfile: profile})
	accountID := fmt.Sprintf("bili-%d", info.UID)
	name := requestedName
	if name == "" {
		name = info.Name
	}
	account := domain.Account{ID: accountID, Name: name, Enabled: true, VipStatus: info.IsVip, Credentials: credentials}

	ctx := context.Background()
	existing, existingErr := s.repository.Account(ctx, accountID)
	if existingErr == nil {
		account.Tags = existing.Tags
		account.Credentials.Version = existing.Credentials.Version + 1
		if len(existing.Credentials.DeviceProfile) > 0 {
			account.Credentials.DeviceProfile = existing.Credentials.DeviceProfile
		}
		if existing.Name != "" && name == info.Name {
			account.Name = existing.Name
		}
		if !existing.Enabled {
			log.Printf("[cluster] reactivating disabled account %s (%s)", accountID, account.Name)
		}
		log.Printf("[cluster] account %s (%s) re-logged via %s", accountID, account.Name, source)
	} else if errors.Is(existingErr, sql.ErrNoRows) {
		log.Printf("[cluster] new account %s (%s) added via %s", accountID, account.Name, source)
	} else {
		return AccountLoginResult{}, existingErr
	}
	if err := s.repository.PutAccount(ctx, account, nil); err != nil {
		return AccountLoginResult{}, err
	}
	_, _ = s.accounts.SyncBuyers(context.Background(), accountID)
	_ = s.refreshResources(context.Background())
	return AccountLoginResult{AccountID: accountID, Name: account.Name}, nil
}

func parsePhoneForLogin(phone string) int64 {
	var n int64
	for _, ch := range phone {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int64(ch-'0')
		}
	}
	return n
}

func randomClusterID(prefix string) string {
	var value [12]byte
	_, _ = rand.Read(value[:])
	return prefix + "-" + hex.EncodeToString(value[:])
}
