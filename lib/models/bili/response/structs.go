package _return

import (
	"strconv"
	"time"
)

// SaleFlagInfo represents a ticket sale flag (stock count + display text).
type SaleFlagInfo struct {
	Number      int    `json:"number"`
	DisplayName string `json:"display_name"`
}

// SaleTimeRange represents the start/end time window for a ticket's sale period.
type SaleTimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type TicketSkuScreenID struct {
	ScreenID  int64         `json:"screenId"`
	SkuID     int64         `json:"skuId"`
	Name      string        `json:"name"`
	Desc      string        `json:"desc"`
	Price     int           `json:"price"`
	Flags     SaleFlagInfo  `json:"flags"`
	SaleStat  SaleTimeRange `json:"saleStat"`
	EventTime time.Time     `json:"eventTime"`
	BuyLimit  int           `json:"buyLimit"`
}

type RequestTokenAndPToken struct {
	RequestToken string
	PToken       string
	GaiaToken    string
}

type ProjectInformation struct {
	ProjectID       string
	StartTime       time.Time
	EndTime         time.Time
	IsHotProject    bool
	IsForceRealName bool
	IsNeedContact   bool
	IDBind          int // 0 = 无实名, 1 = 单人实名可买多张票（拆分下单）, 2 = 一票一实名
	ProjectName     string
}

type TicketBuyer struct {
	BuyerType BuyerType
	ID        int64
	Tel       string
	Name      string
}

func (buyer TicketBuyer) Valid() bool {
	if buyer.BuyerType == Ordinary {
		return buyer.Tel != "" && buyer.Name != ""
	}
	if buyer.BuyerType == ForceRealName {
		return buyer.ID > 0
	}
	return false
}
func (buyer TicketBuyer) Compare(a TicketBuyer) bool {
	if buyer.BuyerType != a.BuyerType {
		return false
	}
	if buyer.BuyerType == Ordinary {
		return buyer.Tel == a.Tel && buyer.Name == a.Name
	} else {
		return buyer.ID == a.ID
	}
}

func (buyer TicketBuyer) String() string {
	if buyer.BuyerType == Ordinary {
		return buyer.Name + " (" + buyer.Tel + ")"
	} else {
		return buyer.Name + " (ID: " + strconv.FormatInt(buyer.ID, 10) + ")"
	}
}

type BuyerType int

const (
	Ordinary BuyerType = iota + 1
	ForceRealName
)
