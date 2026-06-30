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
	"net/http"
	"net/url"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
)

type accountLoginSession struct {
	Client    *biliutils.BiliClient
	Jar       *cookiejar.Jar
	QRCodeKey string
	Name      string
	CreatedAt time.Time
}

// AccountLoginStart is returned by BeginAccountLogin.
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
	applyQRLoginURLCookies(session.Jar, state.URL)
	session.Client.SetRefreshToken(state.RefreshToken)
	info, err := session.Client.GetAccountStatus()
	if err != nil {
		return result, err
	}
	if !info.Login || info.UID == 0 {
		return result, fmt.Errorf("bilibili QR login confirmed but account cookies are not available yet")
	}
	profile, _ := json.Marshal(session.Client.ExportDeviceProfile())
	credentials := credentialsFrom(session.Client, session.Jar, domain.Credentials{RefreshToken: state.RefreshToken, Version: 1, DeviceProfile: profile})
	accountID := fmt.Sprintf("bili-%d", info.UID)
	name := session.Name
	if name == "" {
		name = info.Name
	}
	account := domain.Account{ID: accountID, Name: name, Enabled: true, Credentials: credentials}

	// When the user scans a different account (new UID) than what was
	// previously stored under the same ID, treat it as a new account.
	// If the old account was disabled (e.g. login expired), reactivate
	// it with the new credentials.
	ctx := context.Background()
	existing, existingErr := s.repository.Account(ctx, accountID)
	if existingErr == nil {
		// Same account re-logged — preserve custom name, device profile,
		// and re-enable if previously disabled.
		if len(existing.Credentials.DeviceProfile) > 0 {
			account.Credentials.DeviceProfile = existing.Credentials.DeviceProfile
		}
		if existing.Name != "" && name == info.Name {
			account.Name = existing.Name
		}
		if !existing.Enabled {
			log.Printf("[cluster] reactivating disabled account %s (%s)", accountID, account.Name)
		}
		log.Printf("[cluster] account %s (%s) re-logged", accountID, account.Name)
	} else if errors.Is(existingErr, sql.ErrNoRows) {
		// Brand new account.
		log.Printf("[cluster] new account %s (%s) added via QR login", accountID, account.Name)
	} else {
		return result, existingErr
	}
	if err := s.repository.PutAccount(ctx, account, nil); err != nil {
		return result, err
	}
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	result.AccountID = accountID
	_ = s.refreshResources(context.Background())
	// Best-effort import of the account's existing buyers. Keep it out of
	// the login polling response path so a slow buyer endpoint does not leave
	// the UI stuck at the previous QR state after the user has confirmed login.
	go func() {
		syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.accounts.SyncBuyers(syncCtx, accountID); err != nil {
			log.Printf("[cluster] sync buyers after QR login for %s failed: %v", accountID, err)
			return
		}
		if err := s.refreshResources(context.Background()); err != nil {
			log.Printf("[cluster] refresh resources after QR buyer sync for %s failed: %v", accountID, err)
		}
	}()
	return result, nil
}

func applyQRLoginURLCookies(jar *cookiejar.Jar, rawURL string) {
	if jar == nil || rawURL == "" {
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	query := parsed.Query()
	names := []string{"SESSDATA", "bili_jct", "DedeUserID", "DedeUserID__ckMd5", "sid"}
	cookies := make([]*http.Cookie, 0, len(names))
	for _, name := range names {
		value := query.Get(name)
		if value == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:     name,
			Value:    value,
			Domain:   ".bilibili.com",
			Path:     "/",
			Secure:   true,
			HttpOnly: name == "SESSDATA",
		})
	}
	if len(cookies) == 0 {
		return
	}
	for _, raw := range []string{"https://www.bilibili.com/", "https://bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
		u, err := url.Parse(raw)
		if err == nil {
			jar.SetCookies(u, cookies)
		}
	}
}

func randomClusterID(prefix string) string {
	var value [12]byte
	_, _ = rand.Read(value[:])
	return prefix + "-" + hex.EncodeToString(value[:])
}
