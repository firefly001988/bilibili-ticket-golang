package cluster_service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
)

// SetCatalogClient assigns the employer UI's own Bilibili client for
// project lookups and main-account sync.
func (s *ClusterService) SetCatalogClient(client *biliutils.BiliClient) { s.catalog = client }

// SyncMainAccount mirrors the credentials used by the employer UI into the
// account pool. The UID-derived ID makes this converge with an independently
// scanned pool account instead of creating a duplicate.
func (s *ClusterService) SyncMainAccount() error {
	s.mainAccountMu.Lock()
	defer s.mainAccountMu.Unlock()
	if s.catalog == nil {
		return fmt.Errorf("catalog client is unavailable")
	}
	jar, ok := s.catalog.GetCookieJar().(*cookiejar.Jar)
	if !ok {
		return fmt.Errorf("main account cookie jar cannot be exported")
	}
	credentials := credentialsFrom(s.catalog, jar, domain.Credentials{})
	if credentials.Cookies["SESSDATA"] == "" || credentials.Cookies["bili_jct"] == "" {
		return fmt.Errorf("main account is not logged in")
	}
	info, err := s.catalog.GetAccountStatus()
	if err != nil {
		return err
	}
	if info == nil || !info.Login || info.UID == 0 {
		return fmt.Errorf("main account is not logged in")
	}
	ctx := context.Background()
	accountID := fmt.Sprintf("bili-%d", info.UID)
	account := domain.Account{ID: accountID, Name: info.Name, Enabled: true, Credentials: credentials}
	if existing, existingErr := s.repository.Account(ctx, accountID); existingErr == nil {
		account.Name = existing.Name
		if account.Name == "" {
			account.Name = info.Name
		}
		account.Enabled = existing.Enabled
		account.CooldownUntil = existing.CooldownUntil
		account.Credentials.Version = existing.Credentials.Version + 1
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	} else {
		account.Credentials.Version = 1
	}
	if err := s.repository.PutAccount(ctx, account, nil); err != nil {
		return err
	}
	// Once the UID is known, the anonymous legacy migration row is obsolete.
	if err := s.repository.DeleteAccount(ctx, "migrated-account"); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, _ = s.accounts.SyncBuyers(ctx, accountID)
	return s.refreshResources(ctx)
}

// SKUInspection contains the extra metadata that AutoFillSKUMetadata
// writes into the macro task.
type SKUInspection struct {
	EventDay       string                `json:"eventDay"`
	OrderCapacity  int                   `json:"orderCapacity"`
	CapacitySource domain.CapacitySource `json:"capacitySource"`
	SaleStart      time.Time             `json:"saleStart"`
	SaleEnd        time.Time             `json:"saleEnd"`
}

// ProjectCatalog is the public API view of a project for the frontend.
type ProjectCatalog struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	ForceRealName bool         `json:"forceRealName"`
	IDBind        int          `json:"idBind"`
	Start         time.Time    `json:"start"`
	End           time.Time    `json:"end"`
	Tickets       []CatalogSKU `json:"tickets"`
}

// CatalogSKU is a ticket SKU within a project.
type CatalogSKU struct {
	ScreenID      int64     `json:"screenId"`
	SKUID         int64     `json:"skuId"`
	ScreenName    string    `json:"screenName"`
	SKUName       string    `json:"skuName"`
	Price         int       `json:"price"`
	Status        string    `json:"status"`
	EventTime     time.Time `json:"eventTime"`
	SaleStart     time.Time `json:"saleStart"`
	SaleEnd       time.Time `json:"saleEnd"`
	OrderCapacity int       `json:"orderCapacity"`
}

// LoadProject fetches a project catalog from Bilibili for frontend display.
func (s *ClusterService) LoadProject(projectID string) (ProjectCatalog, error) {
	if s.catalog == nil {
		return ProjectCatalog{}, fmt.Errorf("catalog client is unavailable")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectCatalog{}, fmt.Errorf("project id is required")
	}
	info, err := s.catalog.GetProjectInformationNew(projectID)
	if err != nil {
		return ProjectCatalog{}, err
	}
	tickets, err := s.catalog.GetTicketSkuIDsByProjectIDNew(projectID)
	if err != nil {
		return ProjectCatalog{}, err
	}
	result := ProjectCatalog{ID: info.ProjectID, Name: info.ProjectName, ForceRealName: info.IsForceRealName, IDBind: info.IDBind, Start: info.StartTime, End: info.EndTime}
	for _, ticket := range tickets {
		capacity := ticket.BuyLimit
		if capacity <= 0 {
			capacity = 4
		}
		result.Tickets = append(result.Tickets, CatalogSKU{ScreenID: ticket.ScreenID, SKUID: ticket.SkuID, ScreenName: ticket.Name, SKUName: ticket.Desc, Price: ticket.Price, Status: ticket.Flags.DisplayName, EventTime: ticket.EventTime, SaleStart: ticket.SaleStat.Start, SaleEnd: ticket.SaleStat.End, OrderCapacity: capacity})
	}
	return result, nil
}

// InspectSKU returns the event day, sale window, and real-name requirements
// for a specific (project, screen, SKU) triple so the frontend can
// auto-fill macro metadata.
func (s *ClusterService) InspectSKU(projectID, screenID, skuID int64) (SKUInspection, error) {
	if s.catalog == nil {
		return SKUInspection{}, fmt.Errorf("catalog client is unavailable")
	}
	items, err := s.catalog.GetTicketSkuIDsByProjectIDNew(fmt.Sprint(projectID))
	if err != nil {
		return SKUInspection{}, err
	}
	for _, item := range items {
		if item.ScreenID == screenID && item.SkuID == skuID {
			capacity, source := item.BuyLimit, domain.CapacityAPI
			if capacity <= 0 {
				capacity, source = 4, domain.CapacityDefault
			}
			eventDay := ""
			if !item.EventTime.IsZero() {
				eventDay = item.EventTime.Format("2006-01-02")
			}
			return SKUInspection{EventDay: eventDay, OrderCapacity: capacity, CapacitySource: source, SaleStart: item.SaleStat.Start, SaleEnd: item.SaleStat.End}, nil
		}
	}
	return SKUInspection{}, fmt.Errorf("SKU not found")
}
