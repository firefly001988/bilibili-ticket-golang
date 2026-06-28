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
	info, err := session.Client.GetAccountStatus()
	if err != nil {
		return result, err
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
	// Best-effort import of the account's existing buyers. Login remains
	// successful if Bilibili's buyer endpoint is temporarily unavailable.
	_, _ = s.accounts.SyncBuyers(context.Background(), accountID)
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	result.AccountID = accountID
	_ = s.refreshResources(context.Background())
	return result, nil
}

func randomClusterID(prefix string) string {
	var value [12]byte
	_, _ = rand.Read(value[:])
	return prefix + "-" + hex.EncodeToString(value[:])
}
